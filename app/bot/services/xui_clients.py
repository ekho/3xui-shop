from __future__ import annotations

import logging
from dataclasses import dataclass, field
from typing import Any

from py3xui import AsyncApi

logger = logging.getLogger(__name__)

# Клиент-центричный API появился в 3x-ui v3.1+ (клиент — самостоятельная сущность,
# членство в инбаундах — таблица client_inbounds). py3xui 0.7.0 эти эндпоинты не
# оборачивает — ходим сырыми запросами через транспорт py3xui-сессии (тот же приём,
# что чтение настроек подписки в server_pool). Авторизация/ретраи — py3xui.
_BASE = "panel/api/clients"


def _not_found(exception: ValueError) -> bool:
    return "record not found" in str(exception).lower()


@dataclass
class ClientView:
    """Клиент, как его видит панель: сырой payload + список инбаундов-членств.

    raw хранится целиком, потому что update/:email ЗАМЕНЯЕТ запись (не патчит):
    для правки меняем поля в raw и отправляем его же обратно — так не теряются
    поля, о которых бот не знает (password, security, reset и т.д.).
    """

    email: str
    inbound_ids: list[int]
    raw: dict[str, Any]
    used_traffic: int | None = None  # агрегат по всем инбаундам; есть только у get()

    @property
    def enable(self) -> bool:
        return bool(self.raw.get("enable"))

    @property
    def expiry_time(self) -> int:
        return int(self.raw.get("expiryTime") or 0)

    @property
    def total_gb(self) -> int:
        """Лимит трафика в БАЙТАХ (поле панели называется totalGB, но хранит байты)."""
        return int(self.raw.get("totalGB") or 0)

    @property
    def limit_ip(self) -> int:
        return int(self.raw.get("limitIp") or 0)

    @property
    def sub_id(self) -> str:
        return self.raw.get("subId") or ""

    @property
    def group(self) -> str:
        return self.raw.get("group") or ""


class XuiClientsApi:
    """Обёртки /panel/api/clients/* для одного сервера (одной py3xui-сессии)."""

    def __init__(self, api: AsyncApi) -> None:
        # У py3xui транспорт (_url/_get/_post с авторизацией, ретраями и проверкой
        # success) живёт на каждом под-API; берём inbound — он уже используется так же
        # для panel/api/setting/all.
        self._transport = api.inbound

    async def _get(self, endpoint: str) -> Any:
        response = await self._transport._get(self._transport._url(endpoint), {})
        return response.json().get("obj")

    async def _post(self, endpoint: str, data: dict[str, Any]) -> Any:
        response = await self._transport._post(self._transport._url(endpoint), {}, data)
        return response.json().get("obj")

    async def get(self, email: str) -> ClientView | None:
        """Клиент + его inboundIds + агрегированный usedTraffic. None, если не найден."""
        try:
            obj = await self._get(f"{_BASE}/get/{email}")
        except ValueError as exception:
            if _not_found(exception):
                return None
            raise
        if not obj or not obj.get("client"):
            return None
        return ClientView(
            email=email,
            inbound_ids=list(obj.get("inboundIds") or []),
            raw=obj["client"],
            used_traffic=obj.get("usedTraffic"),
        )

    async def traffic(self, email: str) -> tuple[int, int] | None:
        """(up, down) суммарно по всем инбаундам клиента. None, если не найден."""
        try:
            obj = await self._get(f"{_BASE}/traffic/{email}")
        except ValueError as exception:
            if _not_found(exception):
                return None
            raise
        if not obj:
            return None
        return int(obj.get("up") or 0), int(obj.get("down") or 0)

    async def add(self, client: dict[str, Any], inbound_ids: list[int]) -> None:
        """Создать клиента сразу в наборе инбаундов (панель сама цепляет членства)."""
        await self._post(f"{_BASE}/add", {"client": client, "inboundIds": inbound_ids})

    async def update(self, email: str, client: dict[str, Any]) -> None:
        """Заменить запись клиента; панель пропагирует изменения во все его инбаунды.
        client должен быть ПОЛНЫМ payload'ом (см. ClientView.raw), а не диффом.
        """
        await self._post(f"{_BASE}/update/{email}", client)

    async def delete(self, email: str) -> None:
        """Удалить клиента отовсюду (членства, запись, счётчик трафика)."""
        await self._post(f"{_BASE}/del/{email}", {})

    async def attach(self, email: str, inbound_ids: list[int]) -> None:
        if inbound_ids:
            await self._post(f"{_BASE}/{email}/attach", {"inboundIds": inbound_ids})

    async def detach(self, email: str, inbound_ids: list[int]) -> None:
        if inbound_ids:
            await self._post(f"{_BASE}/{email}/detach", {"inboundIds": inbound_ids})

    async def reset_traffic(self, email: str) -> None:
        """Обнулить up/down по всем инбаундам клиента (и снова включить его в xray)."""
        await self._post(f"{_BASE}/resetTraffic/{email}", {})

    async def export(self) -> list[ClientView]:
        """Все клиенты сервера как {client, inboundIds} — один вызов, для reconciler."""
        rows = await self._get(f"{_BASE}/export") or []
        return [
            ClientView(
                email=row.get("client", {}).get("email") or "",
                inbound_ids=list(row.get("inboundIds") or []),
                raw=row.get("client", {}),
            )
            for row in rows
        ]

    # --- панельные группы клиентов (косметика: метка в UI панели, членств не меняет) ---

    async def list_groups(self) -> list[dict[str, Any]]:
        return await self._get(f"{_BASE}/groups") or []

    async def set_group_label(self, group: str, emails: list[str]) -> None:
        if emails:
            await self._post(f"{_BASE}/groups/bulkAdd", {"emails": emails, "group": group})

    async def clear_group_label(self, emails: list[str]) -> None:
        if emails:
            await self._post(f"{_BASE}/groups/bulkRemove", {"emails": emails})

    async def create_group(self, name: str) -> None:
        await self._post(f"{_BASE}/groups/create", {"name": name})

    async def rename_group(self, old_name: str, new_name: str) -> None:
        await self._post(f"{_BASE}/groups/rename", {"oldName": old_name, "newName": new_name})

    async def delete_group(self, name: str) -> None:
        await self._post(f"{_BASE}/groups/delete", {"name": name})
