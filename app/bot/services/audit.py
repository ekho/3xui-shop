from __future__ import annotations

import html
import logging
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from typing import TYPE_CHECKING

from aiogram import Bot
from sqlalchemy.ext.asyncio import async_sessionmaker

from app.bot.utils.constants import ActorType, AuditAction, AuditSource
from app.config import Config
from app.db.models import AuditLog

if TYPE_CHECKING:
    from aiogram.types import User as TelegramUser

    from app.db.models import User

logger = logging.getLogger(__name__)


@dataclass(frozen=True)
class AuditActor:
    """Кто и через какую поверхность совершил действие. Имя — снапшот на момент события."""

    type: ActorType
    source: AuditSource
    id: int | None = None
    name: str | None = None

    @classmethod
    def admin(cls, tg_user: TelegramUser) -> AuditActor:
        return cls(
            type=ActorType.ADMIN,
            source=AuditSource.MAIN_BOT,
            id=tg_user.id,
            name=tg_user.full_name,
        )

    @classmethod
    def support(cls, tg_user: TelegramUser) -> AuditActor:
        return cls(
            type=ActorType.SUPPORT,
            source=AuditSource.SUPPORT_BOT,
            id=tg_user.id,
            name=tg_user.full_name,
        )

    @classmethod
    def system(cls) -> AuditActor:
        return cls(type=ActorType.SYSTEM, source=AuditSource.JOB)


# Человекочитаемая шапка для поста в канал: слаг действия → (эмодзи, подпись).
_ACTION_META: dict[AuditAction, tuple[str, str]] = {
    AuditAction.USER_COMPENSATE: ("🎁", "Компенсация (бонус-дни)"),
    AuditAction.USER_BAN: ("🚫", "Бан (VPN отключён)"),
    AuditAction.USER_UNBAN: ("✅", "Разбан (VPN восстановлен)"),
    AuditAction.USER_TRAFFIC_RESET: ("♻️", "Сброс трафика"),
    AuditAction.USER_MESSAGE: ("✉️", "Сообщение юзеру"),
    AuditAction.APPROVAL_APPROVE: ("👍", "Заявка одобрена"),
    AuditAction.APPROVAL_REJECT: ("👎", "Заявка отклонена"),
    AuditAction.SUPPORT_BAN: ("🔇", "Блок в поддержке"),
    AuditAction.SUPPORT_UNBAN: ("🔊", "Разблок в поддержке"),
    AuditAction.SUPPORT_CLOSE: ("📪", "Тикет закрыт"),
    AuditAction.SYSTEM_UNLIMITED_RESET: ("🗓", "Месячный сброс безлимита"),
    AuditAction.SYSTEM_AUDIT_PRUNE: ("🧹", "Retention: очистка аудит-лога"),
}

_SOURCE_LABEL: dict[AuditSource, str] = {
    AuditSource.MAIN_BOT: "осн. бот",
    AuditSource.SUPPORT_BOT: "саппорт-бот",
    AuditSource.JOB: "джоб",
}


class AuditService:
    """Глобальный аудит-лог мутаций админов/саппорта/джобов.

    БД (`audit_log`) — источник истины: пишем ВСЕГДА и в СОБСТВЕННОЙ сессии, чтобы не
    коммитить незавершённую транзакцию вызывающего и не потерять запись при её откате.
    Канал — вторичное зеркало для realtime/поиска: пост best-effort, содержит ТОЛЬКО
    метаданные (актор/действие/target/хэштеги) и НИКОГДА тело сообщения/приватный payload
    (тела DM живут только в БД под retention).

    Ни одна ошибка аудита не поднимается наружу — само действие важнее записи о нём.
    """

    def __init__(self, config: Config, bot: Bot, session_factory: async_sessionmaker) -> None:
        self.config = config
        self.bot = bot
        self.session_factory = session_factory
        self.channel_id = config.bot.AUDIT_CHANNEL_ID
        self.retention_days = config.bot.AUDIT_RETENTION_DAYS
        logger.info(
            "Audit Service initialized "
            f"(channel={'on' if self.channel_id else 'off'}, retention={self.retention_days}d)."
        )

    # ── низкоуровневая запись ────────────────────────────────────────────────

    async def record(
        self,
        action: AuditAction,
        actor: AuditActor,
        *,
        target: User | int | None = None,
        payload: dict | None = None,
        channel_note: str | None = None,
    ) -> None:
        target_id, target_name = _split_target(target)

        # 1. Источник истины — БД. Собственная сессия, независимая транзакция.
        try:
            async with self.session_factory() as session:
                await AuditLog.add(
                    session=session,
                    actor_type=actor.type.value,
                    actor_id=actor.id,
                    actor_name=actor.name,
                    action=action.value,
                    target_id=target_id,
                    source=actor.source.value,
                    payload=payload,
                )
        except Exception as exception:  # noqa: BLE001 — аудит не должен ронять действие
            logger.critical(f"Audit DB write failed for {action.value}: {exception}")

        # 2. Зеркало в канал — best-effort, только метаданные.
        await self._broadcast(action, actor, target_id, target_name, channel_note)

    async def _broadcast(
        self,
        action: AuditAction,
        actor: AuditActor,
        target_id: int | None,
        target_name: str | None,
        channel_note: str | None,
    ) -> None:
        if not self.channel_id:
            return
        try:
            await self.bot.send_message(
                chat_id=self.channel_id,
                text=self._format_channel(action, actor, target_id, target_name, channel_note),
            )
        except Exception as exception:  # noqa: BLE001 — падение канала не трогает БД/действие
            logger.error(f"Audit channel post failed for {action.value}: {exception}")

    @staticmethod
    def _format_channel(
        action: AuditAction,
        actor: AuditActor,
        target_id: int | None,
        target_name: str | None,
        channel_note: str | None,
    ) -> str:
        emoji, label = _ACTION_META.get(action, ("•", action.value))
        lines = [f"{emoji} <b>{html.escape(label)}</b>"]

        if actor.type is ActorType.SYSTEM:
            lines.append("👤 Система")
        else:
            name = html.escape(actor.name or "—")
            src = _SOURCE_LABEL.get(actor.source, actor.source.value)
            lines.append(f"👤 {name} · <code>{actor.id}</code> · {src}")

        if target_id is not None:
            tgt = f"🎯 <code>{target_id}</code>"
            if target_name:
                tgt += f" · {html.escape(target_name)}"
            lines.append(tgt)

        if channel_note:
            lines.append(html.escape(channel_note))

        lines.append(_hashtags(action, actor.id, target_id))
        return "\n".join(lines)

    # ── семантические хелперы (вызываются из точек мутаций) ───────────────────

    async def compensation(self, actor: AuditActor, target: User, days: int) -> None:
        await self.record(
            AuditAction.USER_COMPENSATE,
            actor,
            target=target,
            payload={"days": days},
            channel_note=f"+{days} дн.",
        )

    async def ban(self, actor: AuditActor, target: User, before: list, after: list) -> None:
        await self.record(
            AuditAction.USER_BAN,
            actor,
            target=target,
            payload={"groups_before": before, "groups_after": after},
            channel_note="🔒 доступ отключён",
        )

    async def unban(self, actor: AuditActor, target: User, before: list, after: list) -> None:
        await self.record(
            AuditAction.USER_UNBAN,
            actor,
            target=target,
            payload={"groups_before": before, "groups_after": after},
            channel_note="🔓 доступ восстановлен",
        )

    async def traffic_reset(self, actor: AuditActor, target: User) -> None:
        await self.record(
            AuditAction.USER_TRAFFIC_RESET,
            actor,
            target=target,
            channel_note="♻️ счётчик обнулён",
        )

    async def message_sent(self, actor: AuditActor, target: User | int, body: str) -> None:
        # Тело — ТОЛЬКО в БД (payload); в канал уходит факт без текста.
        await self.record(
            AuditAction.USER_MESSAGE,
            actor,
            target=target,
            payload={"body": body},
            channel_note="✉️ отправлено сообщение (текст — в БД)",
        )

    async def approval_decision(self, actor: AuditActor, target: User, approved: bool) -> None:
        await self.record(
            AuditAction.APPROVAL_APPROVE if approved else AuditAction.APPROVAL_REJECT,
            actor,
            target=target,
        )

    async def support_ban(self, actor: AuditActor, target: User | int) -> None:
        await self.record(AuditAction.SUPPORT_BAN, actor, target=target)

    async def support_unban(self, actor: AuditActor, target: User | int) -> None:
        await self.record(AuditAction.SUPPORT_UNBAN, actor, target=target)

    async def support_close(self, actor: AuditActor, target: User | int) -> None:
        await self.record(AuditAction.SUPPORT_CLOSE, actor, target=target)

    async def system_event(
        self,
        action: AuditAction,
        *,
        target: User | int | None = None,
        payload: dict | None = None,
        channel_note: str | None = None,
    ) -> None:
        await self.record(
            action, AuditActor.system(), target=target, payload=payload, channel_note=channel_note
        )

    # ── retention ────────────────────────────────────────────────────────────

    async def prune(self) -> int:
        """Удалить события старше retention-окна. Само удаление логируется системным
        событием (иначе дыра в логе неотличима от ручной подчистки). Возвращает счётчик."""
        cutoff = datetime.now(timezone.utc) - timedelta(days=self.retention_days)
        async with self.session_factory() as session:
            deleted = await AuditLog.delete_older_than(session=session, cutoff=cutoff)

        if deleted:
            logger.info(f"[audit-prune] Deleted {deleted} entries older than {self.retention_days}d.")
            # Событие пруна retention НЕ трогает (created_at=сейчас), поэтому цепочка
            # «когда и сколько чистилось» сохраняется дольше окна.
            await self.system_event(
                AuditAction.SYSTEM_AUDIT_PRUNE,
                payload={"deleted": deleted, "retention_days": self.retention_days},
                channel_note=f"удалено {deleted} зап. старше {self.retention_days} дн.",
            )
        return deleted


def _split_target(target: User | int | None) -> tuple[int | None, str | None]:
    if target is None:
        return None, None
    if isinstance(target, int):
        return target, None
    # duck-typing User: tg_id + first_name (без жёсткого импорта модели)
    tg_id = getattr(target, "tg_id", None)
    name = getattr(target, "first_name", None)
    return tg_id, name


def _hashtags(action: AuditAction, actor_id: int | None, target_id: int | None) -> str:
    # Точки в слаге Telegram в хэштег не берёт — заменяем на подчёркивание (#act_user_ban).
    tags = ["#audit", f"#act_{action.value.replace('.', '_')}"]
    if target_id is not None:
        tags.append(f"#uid_{target_id}")
    if actor_id is not None:
        tags.append(f"#by_{actor_id}")
    return " ".join(tags)


_SOURCE_LABEL_BY_VALUE: dict[str, str] = {src.value: label for src, label in _SOURCE_LABEL.items()}


def _entry_detail(action: AuditAction | None, payload: dict | None) -> str | None:
    """Короткая деталь события для строки истории. Для DM показываем превью тела —
    экран истории только для админа (карточка, IsAdmin), утечки оператору нет."""
    payload = payload or {}
    if action is AuditAction.USER_COMPENSATE and payload.get("days") is not None:
        return f"+{payload['days']} дн."
    if action is AuditAction.USER_MESSAGE and payload.get("body"):
        body = " ".join(str(payload["body"]).split())
        preview = body[:80] + ("…" if len(body) > 80 else "")
        return f"✉️ «{html.escape(preview)}»"
    if action is AuditAction.USER_BAN:
        return "🔒 доступ отключён"
    if action is AuditAction.USER_UNBAN:
        return "🔓 доступ восстановлен"
    if action is AuditAction.SYSTEM_UNLIMITED_RESET and payload:
        return f"сброшено {payload.get('reset', '?')}/{payload.get('targets', '?')}"
    return None


def format_audit_entry(entry: AuditLog) -> str:
    """Одна запись аудита -> компактный HTML-блок для истории в карточке юзера."""
    try:
        action = AuditAction(entry.action)
    except ValueError:
        action = None
    emoji, label = _ACTION_META.get(action, ("•", entry.action))
    date = entry.created_at.strftime("%Y-%m-%d %H:%M") if entry.created_at else "—"

    if entry.actor_type == ActorType.SYSTEM.value:
        actor = "Система"
    else:
        name = html.escape(entry.actor_name or "—")
        src = _SOURCE_LABEL_BY_VALUE.get(entry.source, entry.source)
        actor = f"{name} · <code>{entry.actor_id}</code> · {src}"

    line = f"{emoji} <b>{html.escape(label)}</b> · <code>{date}</code>\n    👤 {actor}"
    detail = _entry_detail(action, entry.payload)
    if detail:
        line += f"\n    {detail}"
    return line
