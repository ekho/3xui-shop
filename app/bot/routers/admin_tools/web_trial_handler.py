"""Operator UI for web requests. No legacy client or payment mutations."""
import html
import asyncio
from uuid import uuid4

from aiogram import F, Router
from aiogram.exceptions import TelegramAPIError
from aiogram.fsm.context import FSMContext
from aiogram.fsm.state import State, StatesGroup
from aiogram.types import CallbackQuery, InlineKeyboardButton, InlineKeyboardMarkup, Message

from app.bot.filters import IsAdmin
from app.bot.services.web_trial import WebTrialAdapter, WebTrialAPIError, valid_uuid

router = Router(name=__name__)

class WebTrialStates(StatesGroup):
    reason = State()
    confirm = State()

async def allowed(event) -> bool:
    user = event.from_user
    message = event.message if isinstance(event, CallbackQuery) or hasattr(event, "message") else event
    return bool(user and not user.is_bot and message and message.chat.type == "private" and message.chat.id == user.id and await IsAdmin()(user_id=user.id))

async def telegram(call):
    try: return await asyncio.wait_for(call, 10)
    except (TelegramAPIError, TimeoutError): return None

def confirmation(identifier):
    return InlineKeyboardMarkup(inline_keyboard=[[
        InlineKeyboardButton(text="Подтвердить",callback_data=f"wt1:y:{identifier}"),
        InlineKeyboardButton(text="Отмена",callback_data=f"wt1:x:{identifier}"),
    ]])

async def api_failure(callback, error):
    if error.status == 409 and error.details.get("current_request_status") in ("approved", "rejected"):
        status = "одобрена" if error.details["current_request_status"] == "approved" else "отказано"
        await telegram(callback.answer(f"Решение уже принято: {status}.", show_alert=True))
    elif error.status == 403:
        await telegram(callback.answer("Backend не разрешил действие этому оператору.", show_alert=True))
    else:
        await telegram(callback.answer("Не удалось подтвердить результат. Повторите действие позже.", show_alert=True))

@router.callback_query(F.data.startswith("wt1:"))
async def callback_web_trial(callback: CallbackQuery, state: FSMContext, web_trial: WebTrialAdapter | None) -> None:
    if not await allowed(callback) or web_trial is None or web_trial.disabled:
        await telegram(callback.answer("Действие недоступно.", show_alert=True))
        return
    parts = (callback.data or "").split(":")
    if len(parts) != 3 or parts[0] != "wt1" or parts[1] not in "arscyx" or len(parts[1]) != 1 or not valid_uuid(parts[2]):
        await telegram(callback.answer("Некорректная кнопка.", show_alert=True))
        return
    action, identifier = parts[1:]
    if action in ("a", "r"):
        try:
            await web_trial.decide(identifier, callback.from_user.id, "approve" if action == "a" else "reject", callback.id)
        except WebTrialAPIError as error:
            await api_failure(callback, error)
            return
        await telegram(callback.answer("Решение принято backend."))
        # The durable delivery loop is the sole writer of approval-card state.
        return
    if action in ("s", "c"):
        await state.clear()
        await state.update_data(web_trial_action=action, web_trial_id=identifier, web_trial_key=str(uuid4()))
        await state.set_state(WebTrialStates.reason)
        await telegram(callback.message.answer("Укажите причину обращения поддержки (1–1000 символов).", reply_markup=InlineKeyboardMarkup(inline_keyboard=[[InlineKeyboardButton(text="Отмена",callback_data=f"wt1:x:{identifier}")]])))
        await telegram(callback.answer())
        return
    data = await state.get_data()
    if data.get("web_trial_id") != identifier or await state.get_state() not in (WebTrialStates.reason.state, WebTrialStates.confirm.state):
        await telegram(callback.answer("Подтверждение устарело.", show_alert=True))
        return
    if action == "x":
        await state.clear()
        await telegram(callback.answer("Действие отменено."))
        return
    if await state.get_state() != WebTrialStates.confirm.state:
        await telegram(callback.answer("Сначала укажите причину.", show_alert=True))
        return
    try:
        if data["web_trial_action"] == "s":
            await web_trial.reconsider(identifier, callback.from_user.id, data["web_trial_reason"], data["web_trial_key"])
            message = "Создана новая заявка на рассмотрение."
        else:
            await web_trial.reconcile(identifier, callback.from_user.id, data["web_trial_reason"], data["web_trial_key"])
            message = "Поставлена проверка исходной выдачи."
    except WebTrialAPIError as error:
        await api_failure(callback, error)
        return  # Preserve confirmation and idempotency key after a lost response.
    await state.clear()
    await telegram(callback.answer(message, show_alert=True))
    await telegram(callback.message.edit_reply_markup(reply_markup=None))

@router.message(WebTrialStates.reason, F.text)
async def message_web_trial_reason(message: Message, state: FSMContext, web_trial: WebTrialAdapter | None) -> None:
    if not await allowed(message) or web_trial is None or web_trial.disabled:
        await telegram(message.answer("Действие недоступно."))
        return
    reason = message.text or ""
    if not reason.strip() or len(reason) > 1000:
        await telegram(message.answer("Причина должна содержать от 1 до 1000 символов."))
        return
    data = await state.get_data()
    if not valid_uuid(data.get("web_trial_id")):
        await state.clear()
        await telegram(message.answer("Действие устарело."))
        return
    await state.update_data(web_trial_reason=reason)
    await state.set_state(WebTrialStates.confirm)
    await telegram(message.answer(f"Подтвердите действие поддержки.\nПричина: {html.escape(reason)}", reply_markup=confirmation(data["web_trial_id"])))
