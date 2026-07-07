from __future__ import annotations

from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from .inbound_groups import InboundGroupService
    from .plan import PlanService
    from .server_pool import Connection, ServerPoolService

import logging

from sqlalchemy.ext.asyncio import async_sessionmaker

from app.bot.models import ClientData
from app.bot.utils.constants import BANNED_INBOUND_GROUP
from app.bot.utils.time import (
    add_days_to_timestamp,
    days_to_timestamp,
    get_current_timestamp,
)
from app.config import Config
from app.db.models import Promocode, User

from .inbound_groups import EmptyInboundSetError
from .xui_clients import ClientView, XuiClientsApi

logger = logging.getLogger(__name__)


def gb_to_bytes(gb: int) -> int:
    """G2: тариф хранит лимит в ГБ, 3x-ui — в байтах (totalGB). 0 → 0 (безлимит)."""
    return int(gb) * 1024**3


class VPNService:
    """Работа с клиентами панели через клиент-центричный API v3.4.2 (/panel/api/clients).

    Клиент живёт в НАБОРЕ инбаундов (группы = тег-префиксы, см. InboundGroupService);
    панель сама пропагирует правки во все членства и агрегирует трафик, подписка
    автоматически отдаёт ссылки всех инбаундов с subId клиента.
    """

    def __init__(
        self,
        config: Config,
        session: async_sessionmaker,
        server_pool_service: ServerPoolService,
        plan_service: PlanService,
        inbound_group_service: InboundGroupService,
    ) -> None:
        self.config = config
        self.session = session
        self.server_pool_service = server_pool_service
        self.plan_service = plan_service
        self.inbound_group_service = inbound_group_service
        logger.info("VPN Service initialized.")

    @staticmethod
    def _clients(connection: Connection) -> XuiClientsApi:
        return XuiClientsApi(connection.api)

    async def _resolve_inbounds(self, connection: Connection, groups: list[str]) -> list[int]:
        """Инбаунды набора на сервере юзера. Резолвятся только access-группы
        (banned инбаундов не имеет). Пустой резолв — EmptyInboundSetError:
        опечатка в теге/удалённый инбаунд не должны молча выдавать пустую подписку
        (алерт делает вызывающий слой — шлюз или крон)."""
        access = self.inbound_group_service.access_groups(groups)
        inbound_ids = await self.inbound_group_service.resolve_ids(connection.api, access)
        if not inbound_ids:
            logger.critical(
                f"Inbound groups {groups} resolve to EMPTY set on server "
                f"'{connection.server.name}' — check inbound tags in the panel."
            )
            raise EmptyInboundSetError(groups, connection.server.name)
        return inbound_ids

    async def _enforce_ban(self, clients: XuiClientsApi, user: User) -> None:
        """Принудительно отключить забаненного (bulkDisable — сразу выкидывает из
        работающего xray; конфиги в подписке остаются видны, но не работают —
        проверено live: sub фильтрует только по enable ИНБАУНДА). Только в сторону
        выключения: ручной disable клиента админом в панели бот не перетирает;
        включение — лишь при явном разбане (apply_inbound_groups(enforce_enable=True))."""
        if not self.inbound_group_service.is_banned(user):
            return
        try:
            await clients.set_clients_enabled([str(user.tg_id)], False)
            logger.info(f"Ban enforced for {user.tg_id}: client disabled.")
        except Exception as exception:
            logger.error(f"Failed to enforce ban for {user.tg_id}: {exception}")

    async def _persist_groups(self, user: User, groups: list[str]) -> None:
        async with self.session() as session:
            await User.update(session=session, tg_id=user.tg_id, inbound_groups=list(groups))
        user.inbound_groups = list(groups)

    async def _mirror_group_label(
        self, clients: XuiClientsApi, groups: list[str], email: str
    ) -> None:
        """Метка client.group в панели — косметика для админа (фильтры/bulk в UI).

        Группы создаются ТОЛЬКО в панели, а bulkAdd с новым именем создал бы
        группу — поэтому пишем метку лишь для набора из одной группы и только
        если она в панели существует. Best-effort: падение не валит выдачу.
        """
        # Забаненный зеркалится меткой banned (когорта видна в панели UI);
        # иначе метку получает только одиночный набор.
        if len(self.inbound_group_service.access_groups(groups)) != len(groups):
            label = BANNED_INBOUND_GROUP
        elif len(groups) == 1:
            label = groups[0]
        else:
            return
        try:
            existing = {row.get("name") for row in await clients.list_groups()}
            if label in existing:
                await clients.set_group_label(label, [email])
        except Exception as exception:
            logger.warning(f"Failed to mirror group label for {email}: {exception}")

    async def is_client_exists(self, user: User) -> ClientView | None:
        connection = await self.server_pool_service.get_connection(user)

        if not connection:
            return None

        client = await self._clients(connection).get(str(user.tg_id))

        if client:
            logger.debug(f"Client {user.tg_id} exists on server {connection.server.name}.")
        else:
            logger.critical(f"Client {user.tg_id} not found on server {connection.server.name}.")

        return client

    async def reconcile_from_panel(self, user: User) -> ClientView | None:
        """Найти клиента панели по email=tg_id на ЛЮБОМ сервере пула и «усыновить»
        его в БД бота (server_id + vpn_id=subId).

        Нужно, когда клиент уже есть в панели, а бот про него не знает: сброс/миграция
        БД бота при живой панели, ручное заведение клиента админом, клиенты прежней
        установки. Без этого первая же покупка/триал пошли бы по ветке create — либо
        клиент-дубль на другом сервере пула (осиротив старую подписку), либо тихий
        отказ add/:email на дубликате («оплачено, но не выдано»).

        Идемпотентно и best-effort: недоступность сервера/ошибка записи не валит
        вызывающий флоу (регистрация/оплата), а лишь логируется.
        """
        try:
            await self.server_pool_service.sync_servers()
            connections = self.server_pool_service.all_connections()
        except Exception as exception:
            logger.error(f"reconcile {user.tg_id}: server pool sync failed: {exception}")
            return None

        found_server = None
        found_view = None
        for connection in connections:
            try:
                view = await self._clients(connection).get(str(user.tg_id))
            except Exception as exception:
                logger.warning(
                    f"reconcile {user.tg_id}: lookup on '{connection.server.name}' failed: {exception}"
                )
                continue
            if view is None:
                continue
            if found_view is None:
                found_view, found_server = view, connection.server
            else:
                # Клиент с этим email на НЕСКОЛЬКИХ серверах пула — модель бота знает
                # один server_id. Берём первый; остальные оставляем админу/reconciler'у.
                logger.warning(
                    f"reconcile {user.tg_id}: client also on '{connection.server.name}'; "
                    f"keeping '{found_server.name}'."
                )

        if found_view is None:
            return None

        # vpn_id бота = subId (хвост ссылки подписки; update таргетит по email). Берём
        # существующий subId клиента, чтобы ссылка заработала сразу, а update был
        # идемпотентен; пустой subId → uuid клиента; в крайнем случае — текущий vpn_id.
        adopted_vpn_id = found_view.sub_id or found_view.raw.get("id") or user.vpn_id

        if user.server_id == found_server.id and user.vpn_id == adopted_vpn_id:
            return found_view  # уже привязан — ничего не пишем

        try:
            async with self.session() as session:
                await User.update(
                    session=session,
                    tg_id=user.tg_id,
                    server_id=found_server.id,
                    vpn_id=adopted_vpn_id,
                )
            user.server_id = found_server.id
            user.vpn_id = adopted_vpn_id
            logger.info(
                f"reconcile {user.tg_id}: adopted panel client from '{found_server.name}' "
                f"(subId={adopted_vpn_id}, inbounds={found_view.inbound_ids})."
            )
        except Exception as exception:
            logger.error(f"reconcile {user.tg_id}: failed to persist adoption: {exception}")
            return None

        return found_view

    async def get_limit_ip(self, user: User, client: ClientView) -> int | None:
        return client.limit_ip

    async def get_client_data(self, user: User) -> ClientData | None:
        logger.debug(f"Starting to retrieve client data for {user.tg_id}.")

        connection = await self.server_pool_service.get_connection(user)

        if not connection:
            return None

        try:
            clients = self._clients(connection)
            view = await clients.get(str(user.tg_id))

            if not view:
                logger.critical(
                    f"Client {user.tg_id} not found on server {connection.server.name}."
                )
                return None

            # Трафик и лимит — агрегаты по ВСЕМУ набору инбаундов клиента (панель сама
            # суммирует; отдельный поход по settings инбаундов больше не нужен).
            traffic = await clients.traffic(str(user.tg_id))
            if traffic is not None:
                traffic_up, traffic_down = traffic
            else:
                traffic_up, traffic_down = (view.used_traffic or 0), 0
            traffic_used = traffic_up + traffic_down

            max_devices = -1 if not view.limit_ip else view.limit_ip
            expiry_time = -1 if view.expiry_time == 0 else view.expiry_time
            total_limit = view.total_gb

            if total_limit <= 0:  # безлимит
                traffic_total = -1
                traffic_remaining = -1
            else:
                traffic_total = total_limit
                traffic_remaining = total_limit - traffic_used

            client_data = ClientData(
                max_devices=max_devices,
                traffic_total=traffic_total,
                traffic_remaining=traffic_remaining,
                traffic_used=traffic_used,
                traffic_up=traffic_up,
                traffic_down=traffic_down,
                expiry_time=expiry_time,
            )
            logger.debug(f"Successfully retrieved client data for {user.tg_id}: {client_data}.")
            return client_data
        except Exception as exception:
            logger.error(f"Error retrieving client data for {user.tg_id}: {exception}")
            return None

    async def get_key(self, user: User) -> str | None:
        async with self.session() as session:
            user = await User.get(session=session, tg_id=user.tg_id)

        if not user.server_id or not user.server:
            logger.debug(f"Server ID for user {user.tg_id} not found.")
            return None

        # Базовый URL подписки берётся из настроек самой панели (Server.subscription_url),
        # прочитанных при добавлении сервера. Если пусто — подписка на панели не настроена/не прочитана.
        if not user.server.subscription_url:
            logger.error(
                f"Subscription URL is not set for server '{user.server.name}' "
                f"(user {user.tg_id}). Проверьте настройки подписки в панели."
            )
            return None

        key = f"{user.server.subscription_url}{user.vpn_id}"
        logger.debug(f"Fetched key for {user.tg_id}: {key}.")
        return key

    async def create_client(
        self,
        user: User,
        devices: int,
        duration: int,
        enable: bool = True,
        flow: str = "xtls-rprx-vision",
        total_gb: int = 0,
        groups: list[str] | None = None,
    ) -> bool:
        """Создать клиента сразу во всех инбаундах его набора групп.

        groups=None -> набор юзера из БД или дефолт. Пустой резолв набора
        пробрасывает EmptyInboundSetError (политика fail+алерт).
        """
        logger.info(f"Creating new client {user.tg_id} | {devices} devices {duration} days.")

        await self.server_pool_service.assign_server_to_user(user)
        connection = await self.server_pool_service.get_connection(user)

        if not connection:
            return False

        groups = list(groups or self.inbound_group_service.effective_groups(user))
        inbound_ids = await self._resolve_inbounds(connection, groups)

        # Per-protocol секреты (пароль SS и т.п.) панель генерирует сама для каждого
        # инбаунда набора; uuid и subId задаём свои — на subId собирается подписка.
        new_client = {
            "email": str(user.tg_id),
            "id": user.vpn_id,
            "subId": user.vpn_id,
            "flow": flow,
            "limitIp": devices,
            "totalGB": total_gb,
            "expiryTime": days_to_timestamp(duration),
            # Забаненный остаётся забаненным, что бы ни провижинилось.
            "enable": enable and BANNED_INBOUND_GROUP not in groups,
            "tgId": 0,
        }

        try:
            clients = self._clients(connection)
            await clients.add(new_client, inbound_ids)
            await self._persist_groups(user, groups)
            await self._mirror_group_label(clients, groups, str(user.tg_id))
            logger.info(
                f"Successfully created client for {user.tg_id} "
                f"in inbounds {inbound_ids} (groups: {groups})."
            )
            return True
        except Exception as exception:
            logger.error(f"Error creating client for {user.tg_id}: {exception}")
            return False

    async def update_client(
        self,
        user: User,
        devices: int,
        duration: int,
        replace_devices: bool = False,
        replace_duration: bool = False,
        enable: bool = True,
        flow: str = "xtls-rprx-vision",
        total_gb: int | None = None,
    ) -> bool:
        logger.info(f"Updating client {user.tg_id} | {devices} devices {duration} days.")
        connection = await self.server_pool_service.get_connection(user)

        if not connection:
            return False

        try:
            clients = self._clients(connection)
            view = await clients.get(str(user.tg_id))

            if view is None:
                logger.critical(f"Client {user.tg_id} not found for update.")
                return False

            if not replace_devices:
                devices = view.limit_ip + devices

            current_time = get_current_timestamp()

            if not replace_duration:
                expiry_time_to_use = max(view.expiry_time, current_time)
            else:
                expiry_time_to_use = current_time

            expiry_time = add_days_to_timestamp(timestamp=expiry_time_to_use, days=duration)

            # update/:email заменяет запись, панель пропагирует её во все инбаунды клиента.
            # Опущенные id/password/auth панель сохраняет от текущей записи (не ротирует),
            # поэтому payload строим явно, а не копией сырой записи.
            # M10/P4: total_gb=None → сохранить текущий лимит (промокод/бонус не должен
            # молча снять платный лимит трафика).
            updated_client = {
                "email": str(user.tg_id),
                "subId": user.vpn_id,
                "flow": flow,
                "limitIp": devices,
                "totalGB": view.total_gb if total_gb is None else total_gb,
                "expiryTime": expiry_time,
                # Продление/бонус не разбанивает: enable перекрывается баном.
                "enable": enable and not self.inbound_group_service.is_banned(user),
                "tgId": int(view.raw.get("tgId") or 0),
                "comment": view.raw.get("comment") or "",
            }
            if view.group:
                updated_client["group"] = view.group  # не затирать метку группы

            await clients.update(str(user.tg_id), updated_client)
            logger.info(f"Client {user.tg_id} updated successfully.")
            return True
        except Exception as exception:
            logger.error(f"Error updating client {user.tg_id}: {exception}")
            return False

    async def apply_inbound_groups(
        self, user: User, groups: list[str] | None = None, enforce_enable: bool = False
    ) -> bool:
        """Привести членства клиента к набору групп (diff -> attach/detach).

        Детачим ТОЛЬКО из инбаундов известных групп: ручные прицепки админа к
        инбаундам с «чужими» тегами не трогаются. Пустой резолв — исключение
        (никогда не приводим клиента к пустому набору).

        Бан: banned в наборе -> клиент отключается всегда; ВКЛЮЧАЕТСЯ обратно
        только при enforce_enable=True (явный разбан из админки) — ручной
        disable клиента админом в панели бот сам не перетирает.
        """
        connection = await self.server_pool_service.get_connection(user)

        if not connection:
            return False

        groups = list(groups or self.inbound_group_service.effective_groups(user))
        desired = set(await self._resolve_inbounds(connection, groups))

        clients = self._clients(connection)
        view = await clients.get(str(user.tg_id))
        if view is None:
            logger.critical(f"Client {user.tg_id} not found to apply groups {groups}.")
            return False

        have = set(view.inbound_ids)
        managed = await self.inbound_group_service.managed_inbound_ids(connection.api)
        to_attach = sorted(desired - have)
        to_detach = sorted((have & managed) - desired)

        try:
            await clients.attach(str(user.tg_id), to_attach)
            await clients.detach(str(user.tg_id), to_detach)

            banned = BANNED_INBOUND_GROUP in groups
            if banned and view.enable:
                await clients.set_clients_enabled([str(user.tg_id)], False)
                logger.info(f"User {user.tg_id} banned: client disabled.")
            elif not banned and not view.enable and enforce_enable:
                await clients.set_clients_enabled([str(user.tg_id)], True)
                logger.info(f"User {user.tg_id} unbanned: client enabled.")
        except Exception as exception:
            logger.error(f"Failed to apply groups {groups} for {user.tg_id}: {exception}")
            return False

        await self._persist_groups(user, groups)
        await self._mirror_group_label(clients, groups, str(user.tg_id))

        if to_attach or to_detach:
            logger.info(
                f"Applied groups {groups} for {user.tg_id}: +{to_attach} -{to_detach}."
            )
        return True

    async def reset_traffic(self, user: User) -> bool:
        """G8/m1: обнулить использованный трафик — разом по всем инбаундам клиента."""
        connection = await self.server_pool_service.get_connection(user)
        if not connection:
            return False
        try:
            await self._clients(connection).reset_traffic(str(user.tg_id))
            logger.info(f"Traffic reset for {user.tg_id}.")
            return True
        except Exception as exception:
            logger.error(f"reset_traffic failed for {user.tg_id}: {exception}")
            return False

    def _plan_groups(self, devices: int) -> list[str] | None:
        """Набор групп купленного тарифа (по devices — ключ тарифа). None, если тариф
        не найден (напр. тариф удалили после покупки) — тогда берётся набор юзера."""
        plan = self.plan_service.get_plan(devices)
        return list(plan.inbound_groups) if plan else None

    async def create_subscription(
        self, user: User, devices: int, duration: int, traffic_gb: int = 0
    ) -> bool:
        # Клиент мог уже существовать в панели (email=tg_id), а в БД бота — нет: сброс/
        # миграция БД бота при живой панели, ручное заведение админом, клиенты прежней
        # установки. Усыновляем и ПРОДЛЕВАЕМ существующего, а не создаём заново — иначе
        # add/:email упал бы дубликатом (тихо → «оплачено, но не выдано») либо создал бы
        # клиента-дубль на другом сервере пула, осиротив старую подписку/ссылку.
        if await self.reconcile_from_panel(user) or await self.is_client_exists(user):
            logger.info(f"Client {user.tg_id} already on panel — extending instead of creating.")
            return await self.extend_subscription(
                user=user, devices=devices, duration=duration, traffic_gb=traffic_gb
            )
        return await self.create_client(
            user=user,
            devices=devices,
            duration=duration,
            total_gb=gb_to_bytes(traffic_gb),
            groups=self._plan_groups(devices),
        )

    async def extend_subscription(
        self, user: User, devices: int, duration: int, traffic_gb: int = 0
    ) -> bool:
        ok = await self.update_client(
            user=user,
            devices=devices,
            duration=duration,
            replace_devices=True,
            total_gb=gb_to_bytes(traffic_gb),
        )
        if not ok:
            return False
        # Набор групп мог смениться (правка тарифа) — сходимся к актуальному.
        # Бан при этом сохраняется: тариф задаёт access-группы, banned остаётся в наборе.
        plan_groups = self._plan_groups(devices)
        if plan_groups is not None and self.inbound_group_service.is_banned(user):
            plan_groups = plan_groups + [BANNED_INBOUND_GROUP]
        await self.apply_inbound_groups(user, groups=plan_groups)
        # m7: продление сбрасывает использованный трафик. extend не считать успешным без сброса
        # (иначе после исчерпания лимита юзер продлил, а счётчик остался > лимита → доступа нет).
        if not await self.reset_traffic(user):
            logger.error(f"extend {user.tg_id}: reset_traffic failed after update.")
            return False
        # resetTraffic на стороне панели заодно ВКЛЮЧАЕТ клиента — бан надо переналожить.
        connection = await self.server_pool_service.get_connection(user)
        if connection:
            await self._enforce_ban(self._clients(connection), user)
        return True

    async def change_subscription(
        self, user: User, devices: int, duration: int, traffic_gb: int = 0
    ) -> bool:
        if await self.is_client_exists(user):
            ok = await self.update_client(
                user,
                devices,
                duration,
                replace_devices=True,
                replace_duration=True,
                total_gb=gb_to_bytes(traffic_gb),
            )
            if ok:
                # Смена тарифа = возможно другой набор инбаундов (например, regular -> premium).
                # Бан сохраняется поверх набора нового тарифа.
                plan_groups = self._plan_groups(devices)
                if plan_groups is not None and self.inbound_group_service.is_banned(user):
                    plan_groups = plan_groups + [BANNED_INBOUND_GROUP]
                await self.apply_inbound_groups(user, groups=plan_groups)
                await self.reset_traffic(user)  # смена тарифа — начать новый лимит с чистого счётчика
                # resetTraffic заодно включает клиента — бан переналожить.
                connection = await self.server_pool_service.get_connection(user)
                if connection:
                    await self._enforce_ban(self._clients(connection), user)
            return ok
        return False

    async def process_bonus_days(self, user: User, duration: int, devices: int) -> bool:
        try:
            if await self.is_client_exists(user):
                updated = await self.update_client(user=user, devices=0, duration=duration)
                if updated:
                    logger.info(f"Updated client {user.tg_id} with additional {duration} days(-s).")
                    return True
            else:
                created = await self.create_client(user=user, devices=devices, duration=duration)
                if created:
                    logger.info(f"Created client {user.tg_id} with additional {duration} days(-s)")
                    return True
        except EmptyInboundSetError as exception:
            # Триал/промокод — не платёж: алертить некому прямо тут, но отказ видим
            # (флоу вернёт юзеру ошибку), а reconciler дополнительно поднимет алерт.
            logger.critical(f"Bonus days for {user.tg_id} failed: {exception}")
            return False

        return False

    async def activate_promocode(self, user: User, promocode: Promocode) -> bool:
        # TODO: consider moving to some 'promocode module services' with usage of vpn-service methods.

        async with self.session() as session:
            activated = await Promocode.set_activated(
                session=session,
                code=promocode.code,
                user_id=user.tg_id,
            )

        if not activated:
            logger.critical(f"Failed to activate promocode {promocode.code} for user {user.tg_id}.")
            return False

        logger.info(f"Begun applying promocode ({promocode.code}) to a client {user.tg_id}.")
        success = await self.process_bonus_days(
            user,
            duration=promocode.duration,
            devices=self.config.shop.BONUS_DEVICES_COUNT,
        )

        if success:
            return True

        async with self.session() as session:
            await Promocode.set_deactivated(session=session, code=promocode.code)

        logger.warning(f"Promocode {promocode.code} not activated due to failure.")
        return False
