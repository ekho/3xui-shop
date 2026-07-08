import logging

from aiogram import F, Router
from aiogram.fsm.context import FSMContext
from aiogram.fsm.state import State, StatesGroup
from aiogram.types import CallbackQuery, Message
from aiogram.utils.i18n import gettext as _
from sqlalchemy.ext.asyncio import AsyncSession

from app.bot.filters import IsAdmin
from app.bot.models import ServicesContainer
from app.bot.routers.misc.keyboard import back_keyboard
from app.bot.utils.constants import (
    MAIN_MESSAGE_ID_KEY,
    PLAN_DEVICES_KEY,
    PLAN_PRICES_KEY,
    PLAN_TRAFFIC_GB_KEY,
    Currency,
)
from app.bot.utils.formatting import format_device_count, format_plan_prices
from app.bot.utils.navigation import NavAdminTools
from app.bot.utils.validation import is_valid_client_count, is_valid_traffic_gb, parse_plan_prices
from app.db.models import Plan as PlanModel
from app.db.models import User

from .keyboard import (
    confirm_create_plan_keyboard,
    confirm_delete_plan_keyboard,
    plan_details_keyboard,
    plan_editor_keyboard,
)

logger = logging.getLogger(__name__)
router = Router(name=__name__)


class CreatePlanStates(StatesGroup):
    devices = State()
    traffic_gb = State()
    prices = State()
    confirmation = State()


class EditPlanTrafficStates(StatesGroup):
    traffic_gb = State()


class EditPlanPricesStates(StatesGroup):
    prices = State()


def _format_traffic(traffic_gb: int) -> str:
    return f"{traffic_gb} {_('GB')}" if traffic_gb else _("plan_editor:message:unlimited_traffic")


def _prices_input_hint(durations: list[int]) -> str:
    days = ", ".join(str(d) for d in durations)
    return _("plan_editor:message:prices_format").format(days=days)


def _current_prices_as_input(prices: dict, durations: list[int]) -> str:
    lines = []
    for duration in durations:
        rub = prices.get(Currency.RUB.code, {}).get(duration, 0)
        usd = prices.get(Currency.USD.code, {}).get(duration, 0)
        xtr = prices.get(Currency.XTR.code, {}).get(duration, 0)
        lines.append(f"{duration} {rub} {usd} {xtr}")
    return "\n".join(lines)


@router.callback_query(F.data == NavAdminTools.PLAN_EDITOR, IsAdmin())
async def callback_plan_editor(
    callback: CallbackQuery,
    user: User,
    state: FSMContext,
    services: ServicesContainer,
) -> None:
    logger.info(f"Admin {user.tg_id} opened plan editor.")
    await state.set_state(None)
    plans = services.plan.get_all_plans()

    text = _("plan_editor:message:main")
    if not plans:
        text += _("plan_editor:message:empty")

    await callback.message.edit_text(text=text, reply_markup=plan_editor_keyboard(plans))


# region Create plan
@router.callback_query(F.data == NavAdminTools.CREATE_PLAN, IsAdmin())
async def callback_create_plan(
    callback: CallbackQuery,
    user: User,
    state: FSMContext,
) -> None:
    logger.info(f"Admin {user.tg_id} started creating a plan.")
    await state.set_state(CreatePlanStates.devices)
    await state.update_data({MAIN_MESSAGE_ID_KEY: callback.message.message_id})
    await callback.message.edit_text(
        text=_("plan_editor:message:enter_devices"),
        reply_markup=back_keyboard(NavAdminTools.PLAN_EDITOR),
    )


@router.message(CreatePlanStates.devices, IsAdmin())
async def message_plan_devices(
    message: Message,
    user: User,
    state: FSMContext,
    services: ServicesContainer,
) -> None:
    devices_input = message.text.strip()
    logger.info(f"Admin {user.tg_id} entered devices count: {devices_input}")
    main_message_id = await state.get_value(MAIN_MESSAGE_ID_KEY)

    if not is_valid_client_count(devices_input):
        await services.notification.notify_by_message(
            message=message, text=_("plan_editor:ntf:invalid_devices"), duration=5
        )
        return

    devices = int(devices_input)
    existing_devices = {plan.devices for plan in services.plan.get_all_plans()}
    if devices in existing_devices:
        await services.notification.notify_by_message(
            message=message, text=_("plan_editor:ntf:devices_exists"), duration=5
        )
        return

    await state.update_data({PLAN_DEVICES_KEY: devices})
    await state.set_state(CreatePlanStates.traffic_gb)
    await message.bot.edit_message_text(
        text=_("plan_editor:message:enter_traffic"),
        chat_id=message.chat.id,
        message_id=main_message_id,
        reply_markup=back_keyboard(NavAdminTools.PLAN_EDITOR),
    )


@router.message(CreatePlanStates.traffic_gb, IsAdmin())
async def message_plan_traffic_gb(
    message: Message,
    user: User,
    state: FSMContext,
    services: ServicesContainer,
) -> None:
    traffic_input = message.text.strip()
    logger.info(f"Admin {user.tg_id} entered traffic limit: {traffic_input}")
    main_message_id = await state.get_value(MAIN_MESSAGE_ID_KEY)

    if not is_valid_traffic_gb(traffic_input):
        await services.notification.notify_by_message(
            message=message, text=_("plan_editor:ntf:invalid_traffic"), duration=5
        )
        return

    await state.update_data({PLAN_TRAFFIC_GB_KEY: int(traffic_input)})
    await state.set_state(CreatePlanStates.prices)
    durations = services.plan.get_durations()
    await message.bot.edit_message_text(
        text=_prices_input_hint(durations),
        chat_id=message.chat.id,
        message_id=main_message_id,
        reply_markup=back_keyboard(NavAdminTools.PLAN_EDITOR),
    )


@router.message(CreatePlanStates.prices, IsAdmin())
async def message_plan_prices(
    message: Message,
    user: User,
    state: FSMContext,
    services: ServicesContainer,
) -> None:
    logger.info(f"Admin {user.tg_id} entered prices for a new plan.")
    main_message_id = await state.get_value(MAIN_MESSAGE_ID_KEY)
    durations = services.plan.get_durations()

    prices = parse_plan_prices(message.text, durations)
    if prices is None:
        await services.notification.notify_by_message(
            message=message, text=_("plan_editor:ntf:invalid_prices"), duration=8
        )
        return

    await state.update_data({PLAN_PRICES_KEY: prices})
    await state.set_state(CreatePlanStates.confirmation)
    data = await state.get_data()

    summary = _("plan_editor:message:create_confirm").format(
        devices=format_device_count(data[PLAN_DEVICES_KEY]),
        traffic=_format_traffic(data[PLAN_TRAFFIC_GB_KEY]),
        prices=format_plan_prices(
            {code: {int(d): p for d, p in per_duration.items()} for code, per_duration in prices.items()},
            durations,
        ),
    )
    await message.bot.edit_message_text(
        text=summary,
        chat_id=message.chat.id,
        message_id=main_message_id,
        reply_markup=confirm_create_plan_keyboard(),
    )


@router.callback_query(
    F.data == NavAdminTools.CONFIRM_CREATE_PLAN,
    CreatePlanStates.confirmation,
    IsAdmin(),
)
async def callback_confirm_create_plan(
    callback: CallbackQuery,
    user: User,
    session: AsyncSession,
    state: FSMContext,
    services: ServicesContainer,
) -> None:
    data = await state.get_data()
    devices = data[PLAN_DEVICES_KEY]
    logger.info(f"Admin {user.tg_id} confirmed creating plan for {devices} devices.")

    plan = await PlanModel.create(
        session=session,
        devices=devices,
        traffic_gb=data[PLAN_TRAFFIC_GB_KEY],
        prices=data[PLAN_PRICES_KEY],
    )

    await state.set_state(None)

    if plan:
        await services.plan.load()
        await callback_plan_editor(callback=callback, user=user, state=state, services=services)
        await services.notification.notify_by_message(
            message=callback.message,
            text=_("plan_editor:ntf:created_success").format(devices=format_device_count(devices)),
            duration=5,
        )
    else:
        await services.notification.show_popup(
            callback=callback, text=_("plan_editor:popup:create_failed")
        )


# endregion


# region Show / edit / delete plan
@router.callback_query(F.data.startswith(NavAdminTools.SHOW_PLAN), IsAdmin())
async def callback_show_plan(
    callback: CallbackQuery,
    user: User,
    state: FSMContext,
    services: ServicesContainer,
) -> None:
    devices = int(callback.data.split("_")[2])
    logger.info(f"Admin {user.tg_id} opened plan for {devices} devices.")
    await state.set_state(None)

    plan = services.plan.get_plan(devices)
    if not plan:
        await services.notification.show_popup(
            callback=callback, text=_("plan_editor:popup:not_found")
        )
        await callback_plan_editor(callback=callback, user=user, state=state, services=services)
        return

    durations = services.plan.get_durations()
    text = _("plan_editor:message:details").format(
        devices=format_device_count(plan.devices),
        traffic=_format_traffic(plan.traffic_gb),
        groups=", ".join(plan.inbound_groups),
        prices=format_plan_prices(plan.prices, durations),
    )
    await callback.message.edit_text(text=text, reply_markup=plan_details_keyboard(devices))


@router.callback_query(F.data.startswith(NavAdminTools.EDIT_PLAN_TRAFFIC), IsAdmin())
async def callback_edit_plan_traffic(
    callback: CallbackQuery,
    user: User,
    state: FSMContext,
    services: ServicesContainer,
) -> None:
    devices = int(callback.data.split("_")[3])
    logger.info(f"Admin {user.tg_id} started editing traffic for plan {devices}.")

    if not services.plan.get_plan(devices):
        await services.notification.show_popup(
            callback=callback, text=_("plan_editor:popup:not_found")
        )
        return

    await state.set_state(EditPlanTrafficStates.traffic_gb)
    await state.update_data(
        {MAIN_MESSAGE_ID_KEY: callback.message.message_id, PLAN_DEVICES_KEY: devices}
    )
    await callback.message.edit_text(
        text=_("plan_editor:message:enter_traffic"),
        reply_markup=back_keyboard(NavAdminTools.SHOW_PLAN + f"_{devices}"),
    )


@router.message(EditPlanTrafficStates.traffic_gb, IsAdmin())
async def message_edit_plan_traffic(
    message: Message,
    user: User,
    session: AsyncSession,
    state: FSMContext,
    services: ServicesContainer,
) -> None:
    traffic_input = message.text.strip()
    data = await state.get_data()
    devices = data[PLAN_DEVICES_KEY]
    logger.info(f"Admin {user.tg_id} entered new traffic limit {traffic_input} for plan {devices}.")

    if not is_valid_traffic_gb(traffic_input):
        await services.notification.notify_by_message(
            message=message, text=_("plan_editor:ntf:invalid_traffic"), duration=5
        )
        return

    plan = await PlanModel.update(session=session, devices=devices, traffic_gb=int(traffic_input))
    await state.set_state(None)

    if not plan:
        await services.notification.notify_by_message(
            message=message, text=_("plan_editor:ntf:update_failed"), duration=5
        )
        return

    await services.plan.load()
    await show_plan_after_update(message=message, state=state, services=services, devices=devices)
    await services.notification.notify_by_message(
        message=message, text=_("plan_editor:ntf:updated_success"), duration=5
    )


@router.callback_query(F.data.startswith(NavAdminTools.EDIT_PLAN_PRICES), IsAdmin())
async def callback_edit_plan_prices(
    callback: CallbackQuery,
    user: User,
    state: FSMContext,
    services: ServicesContainer,
) -> None:
    devices = int(callback.data.split("_")[3])
    logger.info(f"Admin {user.tg_id} started editing prices for plan {devices}.")

    plan = services.plan.get_plan(devices)
    if not plan:
        await services.notification.show_popup(
            callback=callback, text=_("plan_editor:popup:not_found")
        )
        return

    durations = services.plan.get_durations()
    await state.set_state(EditPlanPricesStates.prices)
    await state.update_data(
        {MAIN_MESSAGE_ID_KEY: callback.message.message_id, PLAN_DEVICES_KEY: devices}
    )
    hint = _prices_input_hint(durations)
    current = _current_prices_as_input(plan.prices, durations)
    await callback.message.edit_text(
        text=f"{hint}\n\n{_('plan_editor:message:current_prices')}\n<code>{current}</code>",
        reply_markup=back_keyboard(NavAdminTools.SHOW_PLAN + f"_{devices}"),
    )


@router.message(EditPlanPricesStates.prices, IsAdmin())
async def message_edit_plan_prices(
    message: Message,
    user: User,
    session: AsyncSession,
    state: FSMContext,
    services: ServicesContainer,
) -> None:
    data = await state.get_data()
    devices = data[PLAN_DEVICES_KEY]
    durations = services.plan.get_durations()
    logger.info(f"Admin {user.tg_id} entered new prices for plan {devices}.")

    prices = parse_plan_prices(message.text, durations)
    if prices is None:
        await services.notification.notify_by_message(
            message=message, text=_("plan_editor:ntf:invalid_prices"), duration=8
        )
        return

    plan = await PlanModel.update(session=session, devices=devices, prices=prices)
    await state.set_state(None)

    if not plan:
        await services.notification.notify_by_message(
            message=message, text=_("plan_editor:ntf:update_failed"), duration=5
        )
        return

    await services.plan.load()
    await show_plan_after_update(message=message, state=state, services=services, devices=devices)
    await services.notification.notify_by_message(
        message=message, text=_("plan_editor:ntf:updated_success"), duration=5
    )


async def show_plan_after_update(
    message: Message, state: FSMContext, services: ServicesContainer, devices: int
) -> None:
    main_message_id = await state.get_value(MAIN_MESSAGE_ID_KEY)
    plan = services.plan.get_plan(devices)
    durations = services.plan.get_durations()
    text = _("plan_editor:message:details").format(
        devices=format_device_count(plan.devices),
        traffic=_format_traffic(plan.traffic_gb),
        groups=", ".join(plan.inbound_groups),
        prices=format_plan_prices(plan.prices, durations),
    )
    await message.bot.edit_message_text(
        text=text,
        chat_id=message.chat.id,
        message_id=main_message_id,
        reply_markup=plan_details_keyboard(devices),
    )


@router.callback_query(F.data.startswith(NavAdminTools.CONFIRM_DELETE_PLAN), IsAdmin())
async def callback_confirm_delete_plan(
    callback: CallbackQuery,
    user: User,
    services: ServicesContainer,
) -> None:
    devices = int(callback.data.split("_")[3])
    logger.info(f"Admin {user.tg_id} requested deletion of plan {devices}.")

    if len(services.plan.get_all_plans()) <= 1:
        await services.notification.show_popup(
            callback=callback, text=_("plan_editor:popup:cannot_delete_last")
        )
        return

    await callback.message.edit_text(
        text=_("plan_editor:message:confirm_delete").format(devices=format_device_count(devices)),
        reply_markup=confirm_delete_plan_keyboard(devices),
    )


@router.callback_query(F.data.startswith(NavAdminTools.DELETE_PLAN), IsAdmin())
async def callback_delete_plan(
    callback: CallbackQuery,
    user: User,
    session: AsyncSession,
    state: FSMContext,
    services: ServicesContainer,
) -> None:
    devices = int(callback.data.split("_")[2])

    # Защита от гонки/повторного клика: не даём остаться совсем без тарифов.
    if len(services.plan.get_all_plans()) <= 1:
        await services.notification.show_popup(
            callback=callback, text=_("plan_editor:popup:cannot_delete_last")
        )
        return

    deleted = await PlanModel.delete(session=session, devices=devices)
    logger.info(f"Admin {user.tg_id} deleted plan {devices}: {deleted}.")

    if deleted:
        await services.plan.load()
        await callback_plan_editor(callback=callback, user=user, state=state, services=services)
        await services.notification.show_popup(
            callback=callback, text=_("plan_editor:popup:deleted_success")
        )
    else:
        await services.notification.show_popup(
            callback=callback, text=_("plan_editor:popup:delete_failed")
        )


# endregion
