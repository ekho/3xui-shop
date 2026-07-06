from __future__ import annotations

import logging

from py3xui import AsyncApi, Inbound
from sqlalchemy.ext.asyncio import async_sessionmaker

from app.bot.utils.constants import BANNED_INBOUND_GROUP, DEFAULT_INBOUND_GROUPS
from app.db.models import Plan, User

from .xui_clients import XuiClientsApi

logger = logging.getLogger(__name__)


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
    """Группы инбаундов. Источник истины — ПАНЕЛЬ:

    - список групп создаётся/редактируется только на странице Groups панели
      (client_groups); бот его синкает (GET /panel/api/clients/groups);
    - принадлежность инбаунда группе задаётся тегом инбаунда в панели: инбаунд
      входит в группу, если её имя встречается сегментом тега (через дефис),
      например `regular-n2-in-8443-tcp` -> regular, `regular-premium-x` -> обе;
    - бот группами НЕ управляет (не создаёт, не переименовывает, не ретегает) —
      в боте админ управляет только связкой пользователь<->группы.

    Инбаунды, в теге которых нет ни одной панельной группы, для бота невидимы —
    их членства reconciler не трогает.
    """

    def __init__(self, session_factory: async_sessionmaker) -> None:
        self.session_factory = session_factory
        logger.info("Inbound Group Service initialized.")

    # --- маппинг тегов ---

    @staticmethod
    def groups_of(tag: str, known: set[str]) -> set[str]:
        """Группы инбаунда: дефис-сегменты тега, совпадающие с панельными группами."""
        if not tag:
            return set()
        return {segment for segment in tag.split("-") if segment in known}

    @staticmethod
    def effective_groups(user: User) -> list[str]:
        """Набор групп юзера; None/пусто -> дефолт."""
        return list(user.inbound_groups or DEFAULT_INBOUND_GROUPS)

    @staticmethod
    def access_groups(groups: list[str]) -> list[str]:
        """Группы, дающие доступ: набор без спец-группы бана (по ним резолвятся
        инбаунды; banned инбаундов не имеет и в резолве не участвует)."""
        return [group for group in groups if group != BANNED_INBOUND_GROUP]

    @staticmethod
    def is_banned(user: User) -> bool:
        return BANNED_INBOUND_GROUP in (user.inbound_groups or [])

    # --- синк списка групп из панели ---

    async def known_groups(self, api: AsyncApi) -> set[str]:
        """Группы, определённые в панели ЭТОГО сервера (страница Groups)."""
        try:
            rows = await XuiClientsApi(api).list_groups()
        except Exception as exception:
            logger.error(f"Failed to sync group list from panel: {exception}")
            return set()
        return {row.get("name") for row in rows if row.get("name")}

    async def known_groups_union(self, server_pool) -> set[str]:
        """Объединение групп всех панелей пула — для экранов админки и валидации
        тарифов (наборы юзера/тарифа не привязаны к конкретному серверу)."""
        union: set[str] = set()
        for connection in server_pool.all_connections():
            union |= await self.known_groups(connection.api)
        return union

    async def references(self, name: str) -> tuple[int, int]:
        """(число юзеров с группой в наборе, число тарифов с группой) — для обзора."""
        async with self.session_factory() as session:
            users = await User.get_all(session)
            plans = await Plan.get_all(session)
        user_refs = sum(1 for user in users if name in (user.inbound_groups or []))
        plan_refs = sum(1 for plan in plans if name in (plan.inbound_groups or []))
        return user_refs, plan_refs

    # --- резолв состава по тегам панели ---

    async def resolve(self, api: AsyncApi, groups: list[str]) -> dict[str, list[int]]:
        """{группа: [inbound_id]} для запрошенных групп; только включённые инбаунды.

        Матчим только против групп, реально существующих в панели: имя, которого
        нет на странице Groups, не образует группу, даже если встречается в теге.
        """
        known = await self.known_groups(api)
        wanted = set(groups)
        result: dict[str, list[int]] = {name: [] for name in wanted}
        for inbound in await self._inbounds(api):
            if not inbound.enable:
                continue
            for group in self.groups_of(inbound.tag or "", known) & wanted:
                result[group].append(inbound.id)
        return result

    async def resolve_ids(self, api: AsyncApi, groups: list[str]) -> list[int]:
        """Объединение инбаундов всех групп набора (профиль = union групп)."""
        resolved = await self.resolve(api, groups)
        ids = {inbound_id for id_list in resolved.values() for inbound_id in id_list}
        return sorted(ids)

    async def managed_inbound_ids(self, api: AsyncApi) -> set[int]:
        """Инбаунды, которыми бот управляет: тег содержит хотя бы одну панельную
        группу. Только в их пределах reconciler имеет право детачить.
        """
        known = await self.known_groups(api)
        return {
            inbound.id
            for inbound in await self._inbounds(api)
            if self.groups_of(inbound.tag or "", known)
        }

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
