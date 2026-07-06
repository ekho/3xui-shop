from __future__ import annotations

import logging
import re

from py3xui import AsyncApi, Inbound
from sqlalchemy.ext.asyncio import async_sessionmaker

from app.bot.utils.constants import DEFAULT_INBOUND_GROUPS, INBOUND_GROUP_NAME_PATTERN
from app.db.models import InboundGroup, Plan, User

logger = logging.getLogger(__name__)

_NAME_RE = re.compile(INBOUND_GROUP_NAME_PATTERN)


class EmptyInboundSetError(Exception):
    """Набор групп юзера резолвится в ноль инбаундов на его сервере.

    Политика: fail + алерт (никогда не выдавать/не приводить клиента к пустому
    набору) — опечатка в теге или удалённый инбаунд не должны превращаться
    в тихий отвал пользователей. Пробрасывается до шлюза/крона, где уже есть
    механизм алертов разработчику.
    """

    def __init__(self, groups: list[str], server_name: str) -> None:
        self.groups = groups
        self.server_name = server_name
        super().__init__(
            f"Inbound groups {groups} resolve to an empty inbound set on server '{server_name}'"
        )


class InboundGroupService:
    """Группы инбаундов: реестр имён (БД бота) + резолв состава по тегам панели.

    Конвенция: группа = сегмент тега инбаунда до первого дефиса, и только если
    это имя есть в реестре. Инбаунды с незарегистрированным префиксом (например,
    старые `n2-in-8443-tcp`) для бота не существуют — их членства не трогаются.
    """

    def __init__(self, session_factory: async_sessionmaker) -> None:
        self.session_factory = session_factory
        logger.info("Inbound Group Service initialized.")

    # --- конвенция ---

    @staticmethod
    def parse_group(tag: str) -> str | None:
        """`regular-n2-in-8443-tcp` -> `regular`; тег без дефиса группу не несёт."""
        if not tag or "-" not in tag:
            return None
        return tag.split("-", 1)[0]

    @staticmethod
    def is_valid_name(name: str) -> bool:
        return bool(_NAME_RE.match(name or ""))

    @staticmethod
    def effective_groups(user: User) -> list[str]:
        """Набор групп юзера; None/пусто -> дефолт."""
        return list(user.inbound_groups or DEFAULT_INBOUND_GROUPS)

    @staticmethod
    def profile_label(groups: list[str]) -> str:
        """Имя профиля для зеркала в панельную группу клиента (косметика)."""
        return "+".join(sorted(groups))

    # --- реестр ---

    async def known_groups(self) -> set[str]:
        async with self.session_factory() as session:
            return {group.name for group in await InboundGroup.get_all(session)}

    async def ensure_registered(self, names: list[str]) -> None:
        """Авторегистрация имён (из тарифов): тариф — деплой-контролируемый источник;
        опечатка даст группу с 0 инбаундов и сработает политика fail+алерт (видимо),
        а не тихое падение загрузки.
        """
        known = await self.known_groups()
        async with self.session_factory() as session:
            for name in names:
                if name in known or not self.is_valid_name(name):
                    if name not in known:
                        logger.error(f"Skip auto-registering invalid group name '{name}'.")
                    continue
                await InboundGroup.create(session=session, name=name)

    async def references(self, name: str) -> tuple[int, int]:
        """(число юзеров с группой в наборе, число тарифов с группой) — для гардов."""
        async with self.session_factory() as session:
            users = await User.get_all(session)
            plans = await Plan.get_all(session)
        user_refs = sum(1 for user in users if name in (user.inbound_groups or []))
        plan_refs = sum(1 for plan in plans if name in (plan.inbound_groups or []))
        return user_refs, plan_refs

    # --- резолв состава по тегам панели ---

    async def resolve(self, api: AsyncApi, groups: list[str]) -> dict[str, list[int]]:
        """{группа: [inbound_id]} для запрошенных групп; только включённые инбаунды."""
        wanted = set(groups)
        result: dict[str, list[int]] = {name: [] for name in wanted}
        for inbound in await self._inbounds(api):
            group = self.parse_group(inbound.tag)
            if group in wanted and inbound.enable:
                result[group].append(inbound.id)
        return result

    async def resolve_ids(self, api: AsyncApi, groups: list[str]) -> list[int]:
        """Объединение инбаундов всех групп набора (профиль = union групп)."""
        resolved = await self.resolve(api, groups)
        ids = {inbound_id for id_list in resolved.values() for inbound_id in id_list}
        return sorted(ids)

    async def managed_inbound_ids(self, api: AsyncApi) -> set[int]:
        """Инбаунды, которыми бот управляет: тег-префикс есть в реестре.
        Только в их пределах reconciler имеет право детачить.
        """
        known = await self.known_groups()
        return {
            inbound.id
            for inbound in await self._inbounds(api)
            if self.parse_group(inbound.tag) in known
        }

    async def group_inbounds(self, api: AsyncApi) -> dict[str, list[Inbound]]:
        """{группа из реестра: [инбаунды]} — для экранов админки (включая пустые группы)."""
        known = await self.known_groups()
        result: dict[str, list[Inbound]] = {name: [] for name in known}
        for inbound in await self._inbounds(api):
            group = self.parse_group(inbound.tag)
            if group in known:
                result[group].append(inbound)
        return result

    # --- состав группы: ретег инбаундов в панели ---
    # Включение/исключение инбаунда = смена его тега (read-modify-write через
    # py3xui inbound.update). Update инбаунда перезапускает xray; правила
    # xray-роутинга по старому inboundTag бот не трогает — ответственность админа.
    # Возврат: None = успех, иначе код ошибки для i18n-сообщения хендлера.

    @staticmethod
    def _base_tag(tag: str, known: set[str]) -> str:
        """Тег без известного группового префикса (если он есть)."""
        group = InboundGroupService.parse_group(tag)
        if group in known and "-" in tag:
            return tag.split("-", 1)[1]
        return tag

    async def add_inbound_to_group(self, api: AsyncApi, inbound_id: int, group: str) -> str | None:
        known = await self.known_groups()
        if group not in known:
            return "unknown_group"

        inbound = next((i for i in await self._inbounds(api) if i.id == inbound_id), None)
        if inbound is None:
            return "inbound_not_found"

        base = self._base_tag(inbound.tag or "", known) or f"in-{inbound.id}"
        new_tag = f"{group}-{base}"
        if new_tag == inbound.tag:
            return None

        return await self._retag(api, inbound, new_tag)

    async def remove_inbound_from_group(
        self, api: AsyncApi, inbound_id: int, group: str
    ) -> str | None:
        known = await self.known_groups()
        inbound = next((i for i in await self._inbounds(api) if i.id == inbound_id), None)
        if inbound is None:
            return "inbound_not_found"
        if self.parse_group(inbound.tag or "") != group:
            return None  # уже не в этой группе

        return await self._retag(api, inbound, self._base_tag(inbound.tag, known))

    @staticmethod
    async def _retag(api: AsyncApi, inbound: Inbound, new_tag: str) -> str | None:
        old_tag = inbound.tag
        inbound.tag = new_tag
        try:
            await api.inbound.update(inbound.id, inbound)
        except Exception as exception:
            inbound.tag = old_tag
            # В т.ч. нарушение уникальности тега — панель вернёт ошибку, состав не меняется.
            logger.error(f"Failed to retag inbound {inbound.id} '{old_tag}'->'{new_tag}': {exception}")
            return "api_error"
        logger.info(f"Inbound {inbound.id} retagged '{old_tag}' -> '{new_tag}'.")
        return None

    # --- каскады rename/delete ---

    async def rename_group_cascade(self, server_pool, old_name: str, new_name: str) -> str | None:
        """Переименовать группу: ретег инбаундов на всех серверах -> наборы юзеров ->
        реестр -> зеркало в панели (best-effort). Упавший на середине каскад чинится
        повторным rename (все шаги идемпотентны).
        """
        if not self.is_valid_name(new_name):
            return "invalid_name"
        known = await self.known_groups()
        if old_name not in known:
            return "not_found"
        if new_name in known:
            return "exists"
        _, plan_refs = await self.references(old_name)
        if plan_refs:
            # Иначе авторегистрация из тарифов воскресит старое имя на следующем старте.
            return "referenced_by_plans"

        for connection in server_pool.all_connections():
            for inbound in await self._inbounds(connection.api):
                if self.parse_group(inbound.tag or "") == old_name:
                    error = await self._retag(
                        connection.api, inbound, f"{new_name}-{inbound.tag.split('-', 1)[1]}"
                    )
                    if error:
                        return "api_error"

        async with self.session_factory() as session:
            for user in await User.get_all(session):
                groups = user.inbound_groups or []
                if old_name in groups:
                    new_groups = sorted({new_name if g == old_name else g for g in groups})
                    await User.update(session=session, tg_id=user.tg_id, inbound_groups=new_groups)

        async with self.session_factory() as session:
            await InboundGroup.rename(session=session, old_name=old_name, new_name=new_name)

        for connection in server_pool.all_connections():
            try:
                from .xui_clients import XuiClientsApi

                await XuiClientsApi(connection.api).rename_group(old_name, new_name)
            except Exception:
                pass  # панельная группа — косметика; могла и не существовать
        return None

    async def delete_group_guarded(self, server_pool, name: str) -> str | None:
        """Удалить группу из реестра. Только полностью пустую: не в тарифах, не в
        наборах юзеров, ни один инбаунд не несёт её префикс (сначала исключить их)."""
        user_refs, plan_refs = await self.references(name)
        if plan_refs:
            return "referenced_by_plans"
        if user_refs:
            return "referenced_by_users"

        for connection in server_pool.all_connections():
            for inbound in await self._inbounds(connection.api):
                if self.parse_group(inbound.tag or "") == name:
                    return "has_inbounds"

        async with self.session_factory() as session:
            if not await InboundGroup.delete_by_name(session=session, name=name):
                return "not_found"

        for connection in server_pool.all_connections():
            try:
                from .xui_clients import XuiClientsApi

                await XuiClientsApi(connection.api).delete_group(name)
            except Exception:
                pass  # плейсхолдера в панели могло не быть
        return None

    async def create_group_registered(self, server_pool, name: str) -> str | None:
        """Создать группу в реестре + плейсхолдер в панелях (best-effort)."""
        if not self.is_valid_name(name):
            return "invalid_name"
        if name in await self.known_groups():
            return "exists"

        async with self.session_factory() as session:
            if not await InboundGroup.create(session=session, name=name):
                return "exists"

        for connection in server_pool.all_connections():
            try:
                from .xui_clients import XuiClientsApi

                await XuiClientsApi(connection.api).create_group(name)
            except Exception:
                pass  # уже существует в панели — не страшно
        return None

    async def all_inbounds(self, api: AsyncApi) -> list[Inbound]:
        """Все инбаунды сервера, включая не принадлежащие группам (для экранов админки)."""
        return await self._inbounds(api)

    @staticmethod
    async def _inbounds(api: AsyncApi) -> list[Inbound]:
        try:
            return await api.inbound.get_list()
        except Exception as exception:
            logger.error(f"Failed to fetch inbounds for group resolution: {exception}")
            return []
