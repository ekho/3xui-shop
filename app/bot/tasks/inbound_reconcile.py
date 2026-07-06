import logging
from datetime import datetime, timedelta, timezone

from apscheduler.schedulers.asyncio import AsyncIOScheduler
from redis.asyncio.client import Redis
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker

from app.bot.services import (
    InboundGroupService,
    NotificationService,
    ServerPoolService,
)
from app.bot.services.xui_clients import XuiClientsApi
from app.db.models import User

logger = logging.getLogger(__name__)

INTERVAL_MINUTES = 60
# Дедуп алертов о пустом наборе: не спамить каждый час, пока админ чинит теги.
EMPTY_SET_ALERT_TTL = timedelta(days=1)
EMPTY_SET_TAG = "#InboundGroupsEmptySet"


async def reconcile_inbound_groups(
    session_factory: async_sessionmaker,
    redis: Redis,
    server_pool_service: ServerPoolService,
    inbound_group_service: InboundGroupService,
    notification_service: NotificationService,
) -> None:
    """Свести членства клиентов к желаемым наборам групп (desired -> actual).

    Желаемое: user.inbound_groups (или дефолт) -> инбаунды по тег-префиксам.
    Фактическое: /clients/export (один вызов на сервер).
    Политики: детач только в пределах инбаундов ИЗВЕСТНЫХ групп (ручные прицепки
    админа к «чужим» инбаундам не трогаются); пустой резолв набора — алерт и skip
    (никого не приводим к нулю); любая ошибка API по серверу/юзеру — skip до
    следующего прогона (никаких решений по частичным данным).
    """
    session: AsyncSession
    async with session_factory() as session:
        users = await User.get_all(session=session)

    users_by_server: dict[int, list[User]] = {}
    for user in users:
        if user.server_id:
            users_by_server.setdefault(user.server_id, []).append(user)

    attached = detached = skipped = 0

    for server_id, server_users in users_by_server.items():
        connection = await server_pool_service.get_connection(server_users[0])
        if not connection:
            logger.warning(f"[reconcile] Server {server_id} unavailable; skip its users.")
            skipped += len(server_users)
            continue

        clients = XuiClientsApi(connection.api)
        try:
            # Список групп синкается с панели этого сервера (страница Groups —
            # единственное место, где группы создаются/редактируются).
            known = sorted(await inbound_group_service.known_groups(connection.api))
            group_map = await inbound_group_service.resolve(connection.api, known)
            managed = await inbound_group_service.managed_inbound_ids(connection.api)
            by_email = {view.email: view for view in await clients.export()}
        except Exception as exception:
            logger.error(f"[reconcile] Failed to read server {connection.server.name}: {exception}")
            skipped += len(server_users)
            continue

        for user in server_users:
            groups = inbound_group_service.effective_groups(user)
            desired = {
                inbound_id for name in groups for inbound_id in group_map.get(name, [])
            }

            if not desired:
                # Политика fail+алерт: опечатка/переименованный тег не должны тихо
                # превратиться в отвал юзеров. Membership не трогаем.
                key = f"reconcile:emptyset:{user.tg_id}:{'+'.join(sorted(groups))}"
                if not await redis.get(key):
                    await notification_service.notify_developer(
                        text=(
                            f"{EMPTY_SET_TAG}\n\n"
                            f"User {user.tg_id}: inbound groups {groups} resolve to an EMPTY "
                            f"set on server '{connection.server.name}'. Check inbound tags "
                            f"in the panel (prefix = group name)."
                        )
                    )
                    await redis.set(key, "1", ex=EMPTY_SET_ALERT_TTL)
                skipped += 1
                continue

            view = by_email.get(str(user.tg_id))
            if view is None:
                # Клиента нет на панели вовсе — это не наша зона (создание идёт через
                # покупку/триал с известным сроком и лимитом); просто фиксируем.
                logger.debug(f"[reconcile] Client {user.tg_id} not present on panel; skip.")
                continue

            have = set(view.inbound_ids)
            to_attach = sorted(desired - have)
            to_detach = sorted((have & managed) - desired)
            if not to_attach and not to_detach:
                continue

            try:
                await clients.attach(str(user.tg_id), to_attach)
                await clients.detach(str(user.tg_id), to_detach)
                attached += len(to_attach)
                detached += len(to_detach)
                logger.info(
                    f"[reconcile] {user.tg_id} (groups {groups}): +{to_attach} -{to_detach}."
                )
            except Exception as exception:
                logger.error(f"[reconcile] Failed for {user.tg_id}: {exception}")
                skipped += 1

    logger.info(
        f"[reconcile] Done: +{attached} attach, -{detached} detach, {skipped} skipped."
    )


def start_scheduler(
    session_factory: async_sessionmaker,
    redis: Redis,
    server_pool_service: ServerPoolService,
    inbound_group_service: InboundGroupService,
    notification_service: NotificationService,
) -> None:
    scheduler = AsyncIOScheduler()
    scheduler.add_job(
        reconcile_inbound_groups,
        "interval",
        minutes=INTERVAL_MINUTES,
        args=[
            session_factory,
            redis,
            server_pool_service,
            inbound_group_service,
            notification_service,
        ],
        next_run_time=datetime.now(tz=timezone.utc),
        coalesce=True,
        misfire_grace_time=600,
        max_instances=1,
    )
    scheduler.start()
