from aiogram import Dispatcher

from . import admin, user


def include(dispatcher: Dispatcher) -> None:
    # admin раньше user: у admin-роутера жёсткий фильтр по группе, у user — по личке;
    # пересечений нет, но порядок фиксируем осознанно.
    dispatcher.include_routers(
        admin.router,
        user.router,
    )
