from __future__ import annotations

from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from .server_pool import ServerPoolService

import logging

from py3xui import Client, Inbound
from sqlalchemy.ext.asyncio import async_sessionmaker

from app.bot.models import ClientData
from app.bot.utils.network import extract_base_url
from app.bot.utils.time import (
    add_days_to_timestamp,
    days_to_timestamp,
    get_current_timestamp,
)
from app.config import Config
from app.db.models import Promocode, User

logger = logging.getLogger(__name__)


def gb_to_bytes(gb: int) -> int:
    """G2: тариф хранит лимит в ГБ, 3x-ui — в байтах (totalGB). 0 → 0 (безлимит)."""
    return int(gb) * 1024**3


class VPNService:
    def __init__(
        self,
        config: Config,
        session: async_sessionmaker,
        server_pool_service: ServerPoolService,
    ) -> None:
        self.config = config
        self.session = session
        self.server_pool_service = server_pool_service
        logger.info("VPN Service initialized.")

    @staticmethod
    async def _get_by_email(api, email: str) -> Client | None:
        """P3: py3xui 0.7.0 + 3x-ui v3.1+ БРОСАЕТ ValueError 'record not found' для
        несуществующего клиента вместо возврата None. Приводим к None, остальные ошибки пробрасываем.
        """
        try:
            return await api.client.get_by_email(email)
        except ValueError as exception:
            if "record not found" in str(exception).lower():
                return None
            raise

    async def get_client_settings(self, connection, email: str) -> tuple[int | None, int]:
        """P4: (limit_ip, total_gb в байтах) читаются из settings инбаунда. На 3x-ui v3.4.2
        client.total из get_by_email всегда 0 — настоящий лимит трафика лежит в settings.totalGB
        (как limit_ip). Возвращает (None, 0), если клиент не найден в инбаундах.
        """
        try:
            inbounds: list[Inbound] = await connection.api.inbound.get_list()
        except Exception as exception:
            logger.error(f"Failed to fetch inbounds: {exception}")
            return None, 0
        for inbound in inbounds:
            for inbound_client in inbound.settings.clients:
                if inbound_client.email == email:
                    return inbound_client.limit_ip, (inbound_client.total_gb or 0)
        logger.warning(f"Client {email} not found in inbounds settings.")
        return None, 0

    async def is_client_exists(self, user: User) -> Client | None:
        connection = await self.server_pool_service.get_connection(user)

        if not connection:
            return None

        client = await self._get_by_email(connection.api, str(user.tg_id))

        if client:
            logger.debug(f"Client {user.tg_id} exists on server {connection.server.name}.")
        else:
            logger.critical(f"Client {user.tg_id} not found on server {connection.server.name}.")

        return client

    async def get_limit_ip(self, user: User, client: Client) -> int | None:
        connection = await self.server_pool_service.get_connection(user)

        if not connection:
            return None

        try:
            inbounds: list[Inbound] = await connection.api.inbound.get_list()
        except Exception as exception:
            logger.error(f"Failed to fetch inbounds: {exception}")
            return None

        for inbound in inbounds:
            for inbound_client in inbound.settings.clients:
                if inbound_client.email == client.email:
                    logger.debug(f"Client {client.email} limit ip: {inbound_client.limit_ip}")
                    return inbound_client.limit_ip

        logger.critical(f"Client {client.email} not found in inbounds.")
        return None

    async def get_client_data(self, user: User) -> ClientData | None:
        logger.debug(f"Starting to retrieve client data for {user.tg_id}.")

        connection = await self.server_pool_service.get_connection(user)

        if not connection:
            return None

        try:
            client = await self._get_by_email(connection.api, str(user.tg_id))

            if not client:
                logger.critical(
                    f"Client {user.tg_id} not found on server {connection.server.name}."
                )
                return None

            # P4: limit_ip И лимит трафика читаем из settings (client.total из API v3.4.2 = 0).
            limit_ip, total_limit = await self.get_client_settings(connection, client.email)
            max_devices = -1 if not limit_ip else limit_ip
            expiry_time = -1 if client.expiry_time == 0 else client.expiry_time
            traffic_used = client.up + client.down

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
                traffic_up=client.up,
                traffic_down=client.down,
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

        if not user.server_id:
            logger.debug(f"Server ID for user {user.tg_id} not found.")
            return None

        subscription = extract_base_url(
            url=user.server.host,
            port=self.config.xui.SUBSCRIPTION_PORT,
            path=self.config.xui.SUBSCRIPTION_PATH,
        )
        key = f"{subscription}{user.vpn_id}"
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
        inbound_id: int = 1,
    ) -> bool:
        logger.info(f"Creating new client {user.tg_id} | {devices} devices {duration} days.")

        await self.server_pool_service.assign_server_to_user(user)
        connection = await self.server_pool_service.get_connection(user)

        if not connection:
            return False

        new_client = Client(
            email=str(user.tg_id),
            enable=enable,
            id=user.vpn_id,
            expiry_time=days_to_timestamp(duration),
            flow=flow,
            limit_ip=devices,
            sub_id=user.vpn_id,
            total_gb=total_gb,
        )
        inbound_id = await self.server_pool_service.get_inbound_id(connection.api)

        try:
            await connection.api.client.add(inbound_id=inbound_id, clients=[new_client])
            logger.info(f"Successfully created client for {user.tg_id}")
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
            client = await self._get_by_email(connection.api, str(user.tg_id))  # P3

            if client is None:
                logger.critical(f"Client {user.tg_id} not found for update.")
                return False

            # P4: текущие limit_ip и лимит трафика — из settings (client.* из API v3.4.2 ненадёжны).
            current_device_limit, current_total_gb = await self.get_client_settings(
                connection, client.email
            )
            if not replace_devices:
                devices = (current_device_limit or 0) + devices

            current_time = get_current_timestamp()

            if not replace_duration:
                expiry_time_to_use = max(client.expiry_time, current_time)
            else:
                expiry_time_to_use = current_time

            expiry_time = add_days_to_timestamp(timestamp=expiry_time_to_use, days=duration)

            client.enable = enable
            client.id = user.vpn_id
            client.expiry_time = expiry_time
            client.flow = flow
            client.limit_ip = devices
            client.sub_id = user.vpn_id
            # M10/P4: total_gb=None → сохранить ТЕКУЩИЙ лимит из settings (current_total_gb),
            # а НЕ client.total (на v3.4.2 он = 0 → промокод/бонус молча снял бы платный лимит).
            client.total_gb = (current_total_gb or 0) if total_gb is None else total_gb

            await connection.api.client.update(client_uuid=client.id, client=client)
            logger.info(f"Client {user.tg_id} updated successfully.")
            return True
        except Exception as exception:
            logger.error(f"Error updating client {user.tg_id}: {exception}")
            return False

    async def reset_traffic(self, user: User) -> bool:
        """G8/m1: обнулить использованный трафик. В py3xui 0.7.0 метод — client.reset_stats(inbound_id, email)."""
        connection = await self.server_pool_service.get_connection(user)
        if not connection:
            return False
        inbound_id = await self.server_pool_service.get_inbound_id(connection.api)
        if inbound_id is None:
            logger.error(f"reset_traffic {user.tg_id}: inbound_id not found.")
            return False
        try:
            await connection.api.client.reset_stats(inbound_id=inbound_id, email=str(user.tg_id))
            logger.info(f"Traffic reset for {user.tg_id}.")
            return True
        except Exception as exception:
            logger.error(f"reset_traffic failed for {user.tg_id}: {exception}")
            return False

    async def create_subscription(
        self, user: User, devices: int, duration: int, traffic_gb: int = 0
    ) -> bool:
        if not await self.is_client_exists(user):
            return await self.create_client(
                user=user, devices=devices, duration=duration, total_gb=gb_to_bytes(traffic_gb)
            )
        return False

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
        # m7: продление сбрасывает использованный трафик. extend не считать успешным без сброса
        # (иначе после исчерпания лимита юзер продлил, а счётчик остался > лимита → доступа нет).
        if not await self.reset_traffic(user):
            logger.error(f"extend {user.tg_id}: reset_traffic failed after update.")
            return False
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
                await self.reset_traffic(user)  # смена тарифа — начать новый лимит с чистого счётчика
            return ok
        return False

    async def process_bonus_days(self, user: User, duration: int, devices: int) -> bool:
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
