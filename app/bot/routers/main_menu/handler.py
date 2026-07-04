import logging
from datetime import datetime, timezone

from aiogram import Bot, F, Router
from aiogram.filters import Command, CommandObject
from aiogram.fsm.context import FSMContext
from aiogram.fsm.storage.base import StorageKey
from aiogram.fsm.storage.redis import RedisStorage
from aiogram.types import CallbackQuery, Message
from aiogram.utils.i18n import I18n
from aiogram.utils.i18n import gettext as _
from sqlalchemy.ext.asyncio import AsyncSession

from app.bot.filters import IsAdmin
from app.bot.models import ServicesContainer
from app.bot.utils.constants import DEFAULT_LANGUAGE, MAIN_MESSAGE_ID_KEY, ApprovalStatus
from app.bot.utils.navigation import NavMain
from app.config import Config
from app.db.models import Invite, Referral, User

from .keyboard import main_menu_keyboard

logger = logging.getLogger(__name__)
router = Router(name=__name__)


async def process_invite_attribution(session: AsyncSession, user: User, invite_hash: str) -> bool:
    logger.info(f"Checking invite {invite_hash} for user {user.tg_id}")
    try:
        invite = await Invite.get_by_hash(session=session, hash_code=invite_hash)
        if not invite or not invite.is_active:
            logger.info(f"Invalid or inactive invite hash: {invite_hash}")
            return False

        user.source_invite_name = invite.name
        await session.commit()

        await Invite.increment_clicks(session=session, invite_id=invite.id)

        logger.info(f"User {user.tg_id} attributed to invite {invite.name}")
        return True
    except Exception as exception:
        logger.critical(f"Invite attribution error for user {user.tg_id}: {exception}")
        return False


async def process_creating_referral(session: AsyncSession, user: User, referrer_id: int) -> bool:
    logger.info(f"Assigning user {user.tg_id} as a referred to a referrer user {referrer_id}")
    try:
        referrer = await User.get(session=session, tg_id=referrer_id)
        if not referrer or referrer.tg_id == user.tg_id:
            logger.info(
                f"Failed to assign user {user.tg_id} as a referred to a referrer user {referrer_id}."
                f"Invalid string received."
            )
            return False

        await Referral.create(
            session=session, referrer_tg_id=referrer.tg_id, referred_tg_id=user.tg_id
        )
        logger.info(
            f"User {user.tg_id} assigned as referred to a referrer with tg id {referrer.tg_id}"
        )
        return True
    except Exception as exception:
        logger.critical(
            f"Referral creation error for {user.tg_id} (arg: {referrer_id}): {exception}"
        )
        return False


async def notify_admins_new_request(
    services: ServicesContainer, config: Config, i18n: I18n, user: User
) -> None:
    # Импорт внутри функции — избегаем циклической зависимости на уровне модуля.
    from app.bot.routers.admin_tools.approval_handler import approval_keyboard

    admin_ids = set(config.bot.ADMINS) | {config.bot.DEV_ID}
    # Апдейт пришёл в локали нового юзера → админам рендерим на дефолтной локали.
    with i18n.use_locale(DEFAULT_LANGUAGE):
        text = _("approval:admin:new_request").format(
            name=user.first_name, username=user.username or "-", tg_id=user.tg_id
        )
        keyboard = approval_keyboard(user.tg_id)
    for admin_id in admin_ids:
        await services.notification.notify_by_id(
            chat_id=admin_id, text=text, reply_markup=keyboard
        )


@router.message(Command(NavMain.START))
async def command_main_menu(
    message: Message,
    user: User,
    state: FSMContext,
    services: ServicesContainer,
    config: Config,
    session: AsyncSession,
    command: CommandObject,
    is_new_user: bool,
    i18n: I18n,
) -> None:
    logger.info(f"User {user.tg_id} opened main menu page.")
    previous_message_id = await state.get_value(MAIN_MESSAGE_ID_KEY)

    if previous_message_id:
        try:
            await message.bot.delete_message(chat_id=user.tg_id, message_id=previous_message_id)
            logger.debug(f"Main message for user {user.tg_id} deleted.")
        except Exception as exception:
            logger.error(f"Failed to delete main message for user {user.tg_id}: {exception}")
        finally:
            await state.clear()

    if command.args and is_new_user:
        if command.args.isdigit():
            await process_creating_referral(
                session=session, user=user, referrer_id=int(command.args)
            )
        else:
            await process_invite_attribution(session=session, user=user, invite_hash=command.args)

    is_admin = await IsAdmin()(user_id=user.tg_id)

    # G1: апрув-гейт. Админы авто-approved; остальные не-approved видят статус и блокируются.
    if is_admin:
        if user.approval_status != ApprovalStatus.APPROVED:
            await User.update(session, tg_id=user.tg_id, approval_status=ApprovalStatus.APPROVED)
            user.approval_status = ApprovalStatus.APPROVED
    elif config.shop.APPROVAL_REQUIRED and user.approval_status != ApprovalStatus.APPROVED:
        # M6: уведомляем админов по статусу PENDING с дедупом (не по is_new_user, который сгорает,
        #     если первый апдейт юзера был не /start).
        if user.approval_status == ApprovalStatus.PENDING and user.approval_requested_at is None:
            await notify_admins_new_request(services, config, i18n, user)
            await User.update(
                session, tg_id=user.tg_id, approval_requested_at=datetime.now(timezone.utc)
            )
        await message.answer(
            _("approval:message:pending")
            if user.approval_status == ApprovalStatus.PENDING
            else _("approval:message:rejected")
        )
        return

    main_menu = await message.answer(
        text=_("main_menu:message:main").format(name=user.first_name),
        reply_markup=main_menu_keyboard(
            is_admin,
            is_referral_available=config.shop.REFERRER_REWARD_ENABLED,
            is_trial_available=await services.subscription.is_trial_available(user),
            is_referred_trial_available=await services.referral.is_referred_trial_available(user),
        ),
    )
    await state.update_data({MAIN_MESSAGE_ID_KEY: main_menu.message_id})


@router.callback_query(F.data == NavMain.MAIN_MENU)
async def callback_main_menu(
    callback: CallbackQuery,
    user: User,
    services: ServicesContainer,
    state: FSMContext,
    config: Config,
) -> None:
    logger.info(f"User {user.tg_id} returned to main menu page.")
    await state.clear()
    await state.update_data({MAIN_MESSAGE_ID_KEY: callback.message.message_id})
    is_admin = await IsAdmin()(user_id=user.tg_id)
    await callback.message.edit_text(
        text=_("main_menu:message:main").format(name=user.first_name),
        reply_markup=main_menu_keyboard(
            is_admin,
            is_referral_available=config.shop.REFERRER_REWARD_ENABLED,
            is_trial_available=await services.subscription.is_trial_available(user),
            is_referred_trial_available=await services.referral.is_referred_trial_available(user),
        ),
    )


async def redirect_to_main_menu(
    bot: Bot,
    user: User,
    services: ServicesContainer,
    config: Config,
    storage: RedisStorage | None = None,
    state: FSMContext | None = None,
) -> None:
    logger.info(f"User {user.tg_id} redirected to main menu page.")

    if not state:
        state: FSMContext = FSMContext(
            storage=storage,
            key=StorageKey(bot_id=bot.id, chat_id=user.tg_id, user_id=user.tg_id),
        )

    main_message_id = await state.get_value(MAIN_MESSAGE_ID_KEY)
    is_admin = await IsAdmin()(user_id=user.tg_id)

    try:
        await bot.edit_message_text(
            text=_("main_menu:message:main").format(name=user.first_name),
            chat_id=user.tg_id,
            message_id=main_message_id,
            reply_markup=main_menu_keyboard(
                is_admin,
                is_referral_available=config.shop.REFERRER_REWARD_ENABLED,
                is_trial_available=await services.subscription.is_trial_available(user),
                is_referred_trial_available=await services.referral.is_referred_trial_available(
                    user
                ),
            ),
        )
    except Exception as exception:
        logger.error(f"Error redirecting to main menu page: {exception}")
