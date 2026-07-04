from aiogram.types import InlineKeyboardButton, InlineKeyboardMarkup
from aiogram.utils.i18n import gettext as _
from aiogram.utils.keyboard import InlineKeyboardBuilder

from app.bot.routers.misc.keyboard import back_to_main_menu_button
from app.bot.utils.navigation import NavDownload, NavProfile, NavSubscription
from app.db.models import User


def buy_subscription_keyboard() -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()

    builder.row(
        InlineKeyboardButton(
            text=_("profile:button:buy_subscription"),
            callback_data=NavSubscription.MAIN,
        )
    )

    builder.row(back_to_main_menu_button())
    return builder.as_markup()


def profile_keyboard(user: User | None = None) -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()

    builder.row(
        InlineKeyboardButton(
            text=_("profile:button:show_key"),
            callback_data=NavProfile.SHOW_KEY,
        )
    )
    builder.row(
        InlineKeyboardButton(
            text=_("profile:button:connect"),
            callback_data=NavDownload.MAIN,
        )
    )

    # G4/m4: управление автопродлением Stars
    if user is not None:
        if getattr(user, "is_stars_auto_renew", False):
            builder.row(
                InlineKeyboardButton(
                    text=_("profile:button:cancel_autorenew"),
                    callback_data=NavProfile.CANCEL_STARS_SUB,
                )
            )
        elif getattr(user, "stars_charge_id", None):
            # автопродление было и отменено — можно возобновить, пока период активен
            builder.row(
                InlineKeyboardButton(
                    text=_("profile:button:resume_autorenew"),
                    callback_data=NavProfile.RESUME_STARS_SUB,
                )
            )

    builder.row(back_to_main_menu_button())
    return builder.as_markup()
