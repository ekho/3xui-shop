from __future__ import annotations

from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from .inbound_groups import InboundGroupService
    from .plan import PlanService
    from .server_pool import Connection, ServerPoolService

import logging

from sqlalchemy.ext.asyncio import async_sessionmaker

from app.bot.models import ClientData
from app.bot.utils.constants import (
    BANNED_INBOUND_GROUP,
    DEFAULT_INBOUND_GROUPS,
    UNLIMITED_INBOUND_GROUP,
)
from app.bot.utils.time import (
    add_days_to_timestamp,
    days_to_timestamp,
    get_current_timestamp,
)
from app.config import Config
from app.db.models import Promocode, Server, User

from .inbound_groups import EmptyInboundSetError
from .xui_clients import ClientView, XuiClientsApi

logger = logging.getLogger(__name__)


def gb_to_bytes(gb: int) -> int:
    """G2: тариф хранит лимит в ГБ, 3x-ui — в байтах (totalGB). 0 → 0 (безлимит)."""
    return int(gb) * 1024**3


def client_comment(user: User) -> str:
    """Примечание клиента в панели: «Имя Фамилия / @username», пустые части опускаются.

    Косметика для админа (в UI панели клиент виден только как email=tg_id);
    first_name гарантирован Telegram, last_name/username опциональны.
    """
    name = " ".join(part for part in (user.first_name, user.last_name) if part)
    return f"{name} / @{user.username}" if user.username else name


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

    async def get_available_server(self) -> Server | None:
        """Вернуть сервер, который выбрала бы обычная выдача подписки."""
        return await self.server_pool_service.get_available_server()

    async def _resolve_inbounds(self, connection: Connection, groups: list[str]) -> list[int]:
        """Инбаунды набора на сервере юзера. Резолвятся access-группы с наследованием
        (unlimited ⊇ regular, см. expand_access_groups); banned инбаундов не имеет.
        Пустой резолв — EmptyInboundSetError: опечатка в теге/удалённый инбаунд не
        должны молча выдавать пустую подписку (алерт делает вызывающий слой)."""
        access = self.inbound_group_service.expand_access_groups(groups)
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
        включение — лишь при явном разбане (apply_inbound_groups(enforce_enable=True)).

        ВАЖНО: ручной disable в панели НЕ стойкий — панель включает клиента при
        resetTraffic, а сброс есть во всех платных путях (extend/change) и в сбросе
        из карточки юзера. Единственная стойкая блокировка — бан (группа banned):
        он переналагается после каждого сброса и отменяет Stars-рекуррент."""
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
        его в БД бота (server_id + vpn_id=client id + sub_id=subId).

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

        # Разносим креденшл и подписку (как в 3x-ui): vpn_id = client id (UUID),
        # sub_id = subId (хвост ссылки подписки). Берём оба из записи панели, чтобы
        # ссылка заработала сразу, а update по email был идемпотентен. Фолбэки:
        # пустой id панели → текущий vpn_id; пустой subId → id панели → текущий sub_id.
        adopted_vpn_id = found_view.raw.get("id") or user.vpn_id
        adopted_sub_id = found_view.sub_id or found_view.raw.get("id") or user.sub_id

        if (
            user.server_id == found_server.id
            and user.vpn_id == adopted_vpn_id
            and user.sub_id == adopted_sub_id
        ):
            return found_view  # уже привязан — ничего не пишем

        try:
            async with self.session() as session:
                await User.update(
                    session=session,
                    tg_id=user.tg_id,
                    server_id=found_server.id,
                    vpn_id=adopted_vpn_id,
                    sub_id=adopted_sub_id,
                )
            user.server_id = found_server.id
            user.vpn_id = adopted_vpn_id
            user.sub_id = adopted_sub_id
            logger.info(
                f"reconcile {user.tg_id}: adopted panel client from '{found_server.name}' "
                f"(id={adopted_vpn_id}, subId={adopted_sub_id}, inbounds={found_view.inbound_ids})."
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
                enabled=view.enable,
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

        # Хвост ссылки подписки — subId клиента (как в 3x-ui), т.е. sub_id, а не vpn_id.
        key = f"{user.server.subscription_url}{user.sub_id}"
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
        expiry_override: int | None = None,
    ) -> bool:
        """Создать клиента сразу во всех инбаундах его набора групп.

        groups=None -> набор юзера из БД или дефолт. Пустой резолв набора
        пробрасывает EmptyInboundSetError (политика fail+алерт).
        expiry_override -> явный expiryTime вместо срока по duration (0 = бессрочно,
        для безлимит-плана; иначе days_to_timestamp(duration)).
        """
        logger.info(f"Creating new client {user.tg_id} | {devices} devices {duration} days.")

        await self.server_pool_service.assign_server_to_user(user)
        connection = await self.server_pool_service.get_connection(user)

        if not connection:
            return False

        groups = list(groups or self.inbound_group_service.effective_groups(user))
        inbound_ids = await self._resolve_inbounds(connection, groups)

        # Per-protocol секреты (пароль SS и т.п.) панель генерирует сама для каждого
        # инбаунда набора; id (UUID-креденшл) и subId задаём свои — на subId собирается
        # подписка. Как в 3x-ui, это разные значения: id=vpn_id, subId=sub_id.
        new_client = {
            "email": str(user.tg_id),
            "id": user.vpn_id,
            "subId": user.sub_id,
            "flow": flow,
            "limitIp": devices,
            "totalGB": total_gb,
            "expiryTime": days_to_timestamp(duration) if expiry_override is None else expiry_override,
            # Забаненный остаётся забаненным, что бы ни провижинилось.
            "enable": enable and BANNED_INBOUND_GROUP not in groups,
            "tgId": 0,
            "comment": client_comment(user),
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
        expiry_override: int | None = None,
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
            # Безлимит-план: явный expiryTime (0 = бессрочно) поверх расчёта по duration.
            if expiry_override is not None:
                expiry_time = expiry_override

            # update/:email заменяет запись, панель пропагирует её во все инбаунды клиента.
            # Опущенные id/password/auth панель сохраняет от текущей записи (не ротирует),
            # поэтому payload строим явно, а не копией сырой записи.
            # M10/P4: total_gb=None → сохранить текущий лимит (промокод/бонус не должен
            # молча снять платный лимит трафика).
            updated_client = {
                "email": str(user.tg_id),
                "subId": user.sub_id,
                "flow": flow,
                "limitIp": devices,
                "totalGB": view.total_gb if total_gb is None else total_gb,
                "expiryTime": expiry_time,
                # Продление/бонус не разбанивает: enable перекрывается баном.
                "enable": enable and not self.inbound_group_service.is_banned(user),
                "tgId": int(view.raw.get("tgId") or 0),
                # Ручное примечание админа не перезаписываем; пустое — бэкфиллим
                # своим форматом (клиенты, заведённые до появления comment).
                "comment": view.raw.get("comment") or client_comment(user),
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

    async def grant_unlimited(self, user: User) -> bool:
        """Админ-грант безлимит-плана (скрытый Plan с группой `unlimited`).

        Провижинит клиента лимитами плана: limitIp=plan.devices (7), totalGB=100ГБ,
        expiryTime=0 (бессрочно), членство — группа `unlimited`. Месячный сброс
        трафика делает САМА панель (inbound.TrafficReset=monthly на инбаундах группы),
        поэтому бот здесь ничего не сбрасывает и client.reset не выставляет
        (см. память panel-client-reset-semantics: client.reset — авто-продление по
        истечению, требует expiryTime>0 и потому несовместимо с бессрочным сроком).

        Создаёт клиента, если его ещё нет; иначе переводит существующего. Идемпотентно.
        Бан сохраняется поверх набора. EmptyInboundSetError (группа `unlimited` не
        заведена/не тегирована в панели) -> отказ с явным логом, флоу не валится.
        """
        plan = self.plan_service.get_unlimited_plan()
        if plan is None:
            logger.error(
                f"grant_unlimited {user.tg_id}: безлимит-план не найден — нужен скрытый "
                f"Plan с группой '{UNLIMITED_INBOUND_GROUP}' в inbound_groups."
            )
            return False

        groups = list(plan.inbound_groups)
        total_gb = gb_to_bytes(plan.traffic_gb)  # 0 -> безлимит-трафик; 100 -> 100ГБ-кап

        try:
            # Клиент уже на панели (или усыновляется reconcile) -> обновляем лимиты.
            if await self.reconcile_from_panel(user) or await self.is_client_exists(user):
                ok = await self.update_client(
                    user=user,
                    devices=plan.devices,
                    duration=0,
                    replace_devices=True,
                    replace_duration=True,
                    total_gb=total_gb,
                    expiry_override=0,  # бессрочно
                )
                if not ok:
                    return False
                # Свести членства к набору безлимит-плана; бан остаётся поверх.
                plan_groups = groups
                if self.inbound_group_service.is_banned(user):
                    plan_groups = plan_groups + [BANNED_INBOUND_GROUP]
                applied = await self.apply_inbound_groups(user, groups=plan_groups)
                if applied:
                    logger.info(f"Unlimited plan granted to existing client {user.tg_id}.")
                return applied

            # Нового клиента создаём сразу бессрочным на группе `unlimited`.
            created = await self.create_client(
                user=user,
                devices=plan.devices,
                duration=0,
                total_gb=total_gb,
                groups=groups,
                expiry_override=0,  # бессрочно
            )
            if created:
                logger.info(f"Unlimited plan granted to new client {user.tg_id}.")
            return created
        except EmptyInboundSetError as exception:
            logger.critical(f"grant_unlimited for {user.tg_id} failed: {exception}")
            return False

    async def revoke_unlimited(self, user: User) -> bool:
        """Снять безлимит и откатить клиента на СТАРТОВЫЙ ТРИАЛ.

        Триал = те же параметры, что даёт gift_trial новому юзеру: BONUS_DEVICES_COUNT
        устройств, TRIAL_PERIOD дней, лимит трафика SHOP_TRIAL_TRAFFIC_GB (0 = безлимит),
        дефолтный набор групп (regular). Клиент не удаляется — переводится; группа
        `unlimited` отцепляется. Идём напрямую через update_client, минуя
        SubscriptionService.gift_trial: его гейт is_trial_available требует отсутствия
        сервера у юзера, а у безлимитчика он есть.

        Счётчик трафика сбрасывается (за безлимит юзер мог использовать > лимита триала —
        иначе триал сразу упёрся бы в кап). Бан сохраняется поверх набора.
        EmptyInboundSetError (нет regular-инбаундов на сервере) -> отказ с логом.
        """
        trial_period = self.config.shop.TRIAL_PERIOD
        trial_devices = self.config.shop.BONUS_DEVICES_COUNT
        trial_total_gb = gb_to_bytes(self.config.shop.TRIAL_TRAFFIC_GB)  # 0 -> безлимит
        groups = list(DEFAULT_INBOUND_GROUPS)
        if self.inbound_group_service.is_banned(user):
            groups = groups + [BANNED_INBOUND_GROUP]

        try:
            # Клиента на панели нет (грант не был провижинен) — просто вернуть дефолтный набор.
            if not await self.is_client_exists(user):
                await self._persist_groups(user, groups)
                logger.info(
                    f"Unlimited revoked for {user.tg_id} (no panel client): groups reset to default."
                )
                return True

            ok = await self.update_client(
                user=user,
                devices=trial_devices,
                duration=trial_period,
                replace_devices=True,
                replace_duration=True,
                total_gb=trial_total_gb,
            )
            if not ok:
                return False
            # Свести членства к дефолтному набору (отцепить unlimited); бан остаётся поверх.
            # enforce_enable=True: снятие безлимита — явное действие админа.
            applied = await self.apply_inbound_groups(user, groups=groups, enforce_enable=True)
            if not applied:
                return False
            # Начать триал с чистого счётчика (клиент мог накопить трафик на безлимите).
            if not await self.reset_traffic(user):
                logger.error(f"revoke_unlimited {user.tg_id}: reset_traffic failed after update.")
                return False
            # resetTraffic заодно включает клиента — бан переналожить.
            connection = await self.server_pool_service.get_connection(user)
            if connection:
                await self._enforce_ban(self._clients(connection), user)
            logger.info(
                f"Unlimited revoked for {user.tg_id}: reset to starter trial "
                f"({trial_devices} devices, {trial_period} days, "
                f"{self.config.shop.TRIAL_TRAFFIC_GB}GB, groups {groups})."
            )
            return True
        except EmptyInboundSetError as exception:
            logger.critical(f"revoke_unlimited for {user.tg_id} failed: {exception}")
            return False

    async def compensation_blocker(self, user: User) -> str | None:
        """Гвард начисления бонусных дней админом/оператором. Возвращает код причины
        отказа или None (можно начислять):

        - 'unlimited'/'banned' — по группам в БД (дни превратили бы бессрочный срок в
          конечный / тихо сгорали бы у выключенного клиента);
        - 'server_unreachable' — сервер юзера назначен, но соединения нет: create-ветка
          process_bonus_days иначе тихо пересадила бы юзера на другой сервер пула,
          создав клиента-дубль;
        - 'perpetual' — клиент панели бессрочный (expiryTime=0, напр. заведён руками
          в панели) — max(0, now)+N сделал бы срок конечным;
        - 'no_server' — клиента нет нигде (reconcile не нашёл) и нет сервера со
          свободными местами для создания.

        Побочный эффект: у юзера без server_id пытается усыновить клиента с панели
        (reconcile_from_panel мутирует user in-place) — после None-результата можно
        сразу звать process_bonus_days по update-ветке.
        """
        if self.inbound_group_service.is_unlimited(user):
            return "unlimited"
        if self.inbound_group_service.is_banned(user):
            return "banned"
        if not user.server_id:
            await self.reconcile_from_panel(user)
        if user.server_id:
            connection = await self.server_pool_service.get_connection(user)
            if connection is None:
                return "server_unreachable"
            view = await self._clients(connection).get(str(user.tg_id))
            if view is not None and view.expiry_time == 0:
                return "perpetual"
            return None
        if await self.server_pool_service.get_available_server() is None:
            return "no_server"
        return None

    async def process_bonus_days(
        self, user: User, duration: int, devices: int, traffic_gb: int = 0
    ) -> bool:
        # traffic_gb: лимит трафика в ГБ для выдачи (0 = безлимит). Триал передаёт
        # SHOP_TRIAL_TRAFFIC_GB; промокод/реферер-награда — 0 (не капать бонус).
        # В update-ветке 0 -> None (не трогать текущий лимит платного клиента, M10/P4).
        update_total_gb = gb_to_bytes(traffic_gb) if traffic_gb else None
        try:
            if await self.is_client_exists(user):
                updated = await self.update_client(
                    user=user, devices=0, duration=duration, total_gb=update_total_gb
                )
                if updated:
                    logger.info(f"Updated client {user.tg_id} with additional {duration} days(-s).")
                    return True
            else:
                created = await self.create_client(
                    user=user, devices=devices, duration=duration, total_gb=gb_to_bytes(traffic_gb)
                )
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
