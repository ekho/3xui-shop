import logging
from datetime import datetime
from typing import Any, Self

from sqlalchemy import JSON, delete, func, select
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy.orm import Mapped, mapped_column

from . import Base

logger = logging.getLogger(__name__)


class AuditLog(Base):
    """
    Immutable-by-convention record of a state-changing action by an admin, a support
    operator, or a system job. Source of truth for the audit trail (the Telegram
    channel mirror is a secondary, PII-free copy).

    Scope is deliberately мутации-only: views (/info, statistics) are NOT logged.

    Attributes:
        id (int): Unique primary key.
        created_at (datetime): When the action happened. Indexed — retention prunes by it.
        actor_type (str): admin | support | system (see ActorType).
        actor_id (int | None): tg_id of the admin/operator; None for system jobs.
        actor_name (str | None): Snapshot of the actor's display name at the time (the
            live name may change or the account vanish; forensics needs the then-value).
        action (str): Taxonomy slug, e.g. "user.compensate" (see AuditAction).
        target_id (int | None): tg_id of the affected user; None for user-agnostic events
            (e.g. a system prune). Indexed — powers per-user history (card view, v2).
        source (str): main_bot | support_bot | job (see AuditSource).
        payload (dict | None): Full event detail — before→after, granted days, message
            body, counts. Lives ONLY here (never in the channel), under retention.
    """

    __tablename__ = "audit_log"

    id: Mapped[int] = mapped_column(primary_key=True, autoincrement=True)
    created_at: Mapped[datetime] = mapped_column(default=func.now(), nullable=False, index=True)
    actor_type: Mapped[str] = mapped_column(nullable=False)
    actor_id: Mapped[int | None] = mapped_column(nullable=True)
    actor_name: Mapped[str | None] = mapped_column(nullable=True)
    action: Mapped[str] = mapped_column(nullable=False)
    target_id: Mapped[int | None] = mapped_column(nullable=True, index=True)
    source: Mapped[str] = mapped_column(nullable=False)
    payload: Mapped[dict | None] = mapped_column(JSON, nullable=True)

    def __repr__(self) -> str:
        return (
            f"<AuditLog(id={self.id}, action={self.action}, actor_id={self.actor_id}, "
            f"target_id={self.target_id}, created_at={self.created_at})>"
        )

    @classmethod
    async def add(cls, session: AsyncSession, **kwargs: Any) -> Self | None:
        entry = AuditLog(**kwargs)
        session.add(entry)
        try:
            await session.commit()
            await session.refresh(entry)
            return entry
        except Exception as exception:
            # Аудит не должен ронять само действие: коммит откатываем, но кричим в лог —
            # молчаливая потеря записи в security-логе недопустима.
            await session.rollback()
            logger.critical(f"Failed to persist audit entry {kwargs}: {exception}")
            return None

    @classmethod
    async def get_page(
        cls,
        session: AsyncSession,
        *,
        target_id: int | None = None,
        actor_id: int | None = None,
        limit: int = 20,
        offset: int = 0,
    ) -> list[Self]:
        """Newest-first slice, optionally filtered by target or actor (v2 card history)."""
        query = select(AuditLog)
        if target_id is not None:
            query = query.where(AuditLog.target_id == target_id)
        if actor_id is not None:
            query = query.where(AuditLog.actor_id == actor_id)
        query = query.order_by(AuditLog.created_at.desc()).limit(limit).offset(offset)
        result = await session.execute(query)
        return list(result.scalars().all())

    @classmethod
    async def delete_older_than(cls, session: AsyncSession, cutoff: datetime) -> int:
        """Retention prune: delete rows strictly older than `cutoff`. Returns row count."""
        result = await session.execute(delete(AuditLog).where(AuditLog.created_at < cutoff))
        await session.commit()
        return result.rowcount or 0
