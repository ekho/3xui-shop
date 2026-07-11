import logging

from aiogram import Router
from aiogram.exceptions import TelegramBadRequest
from aiogram.types import CallbackQuery
from aiogram.utils.i18n import gettext as _
from sqlalchemy.ext.asyncio import AsyncSession

from app.bot.filters import IsAdmin
from app.bot.models import ServicesContainer
from app.bot.services.approval import ApprovalCallback
from app.bot.utils.constants import ApprovalStatus
from app.db.models import User

logger = logging.getLogger(__name__)
router = Router(name=__name__)

# Логика заявок (решение, карточки, напоминания, клавиатуры) живёт в
# app/bot/services/approval.py — общем сервисе для основного и support-бота.
# Здесь только реактивные кнопки в карточках, пришедших в личку админам.


@router.callback_query(ApprovalCallback.filter(), IsAdmin())
async def on_approval(
    callback: CallbackQuery,
    callback_data: ApprovalCallback,
    session: AsyncSession,
    services: ServicesContainer,
) -> None:
    new_status = (
        ApprovalStatus.APPROVED
        if callback_data.action == "approve"
        else ApprovalStatus.REJECTED
    )
    target = await User.get(session, tg_id=callback_data.user_id)
    if target is None:
        await callback.answer(_("approval:admin:user_not_found"), show_alert=True)
        return

    applied = await services.approval.apply_decision(
        session, target, new_status, decided_by=callback.from_user.id
    )
    if not applied:
        await callback.answer(_("approval:admin:already_processed"), show_alert=True)
        return

    # сообщение админу — в локали текущего апдейта (админа), это корректно
    try:
        await callback.message.edit_text(
            callback.message.text
            + "\n\n"
            + _("approval:admin:done").format(status=new_status.value)
        )
    except TelegramBadRequest:
        pass  # текст не изменился / сообщение недоступно
    await callback.answer()
