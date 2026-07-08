import logging
from datetime import datetime
from typing import Any, Self

from sqlalchemy import Enum as SQLEnum
from sqlalchemy import func, select, update
from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy.orm import Mapped, mapped_column

from app.bot.utils.constants import SupportTicketStatus

from . import Base

logger = logging.getLogger(__name__)


class SupportTicket(Base):
    """
    Represents a support conversation between a user and the support group.

    Attributes:
        id (int): Unique primary key.
        tg_id (int): Telegram user ID of the ticket owner (one ticket per user).
        thread_id (int | None): Forum topic id in the support group; None until first message
            (topic is created lazily) or after the topic was deleted manually.
        status (SupportTicketStatus): open | closed | banned.
        created_at (datetime): Timestamp when the ticket was created.
        updated_at (datetime): Timestamp of the last status/thread change.
    """

    __tablename__ = "support_tickets"

    id: Mapped[int] = mapped_column(primary_key=True, autoincrement=True)
    tg_id: Mapped[int] = mapped_column(unique=True, nullable=False)
    thread_id: Mapped[int | None] = mapped_column(nullable=True)
    status: Mapped[SupportTicketStatus] = mapped_column(
        SQLEnum(SupportTicketStatus, values_callable=lambda obj: [e.value for e in obj]),
        default=SupportTicketStatus.OPEN,
        nullable=False,
    )
    created_at: Mapped[datetime] = mapped_column(default=func.now(), nullable=False)
    updated_at: Mapped[datetime] = mapped_column(
        default=func.now(), onupdate=func.now(), nullable=False
    )

    def __repr__(self) -> str:
        return (
            f"<SupportTicket(id={self.id}, tg_id={self.tg_id}, thread_id={self.thread_id}, "
            f"status={self.status}, created_at={self.created_at})>"
        )

    @classmethod
    async def get_by_tg_id(cls, session: AsyncSession, tg_id: int) -> Self | None:
        query = await session.execute(select(SupportTicket).where(SupportTicket.tg_id == tg_id))
        return query.scalar_one_or_none()

    @classmethod
    async def get_by_thread_id(cls, session: AsyncSession, thread_id: int) -> Self | None:
        query = await session.execute(
            select(SupportTicket).where(SupportTicket.thread_id == thread_id)
        )
        return query.scalar_one_or_none()

    @classmethod
    async def create(cls, session: AsyncSession, tg_id: int, **kwargs: Any) -> Self | None:
        ticket = await SupportTicket.get_by_tg_id(session=session, tg_id=tg_id)

        if ticket:
            logger.warning(f"Support ticket for user {tg_id} already exists.")
            return ticket

        ticket = SupportTicket(tg_id=tg_id, **kwargs)
        session.add(ticket)

        try:
            await session.commit()
            logger.debug(f"Support ticket for user {tg_id} created.")
            return ticket
        except IntegrityError as exception:
            # Гонка первого контакта (конкурентные апдейты): проигравший INSERT берёт
            # строку победителя — для вызывающего это равнозначно успеху.
            await session.rollback()
            existing = await SupportTicket.get_by_tg_id(session=session, tg_id=tg_id)
            if existing:
                logger.debug(f"Support ticket for user {tg_id} created concurrently; reusing.")
                return existing
            logger.error(f"Error occurred while creating support ticket {tg_id}: {exception}")
            return None

    @classmethod
    async def update(cls, session: AsyncSession, tg_id: int, **kwargs: Any) -> Self | None:
        ticket = await SupportTicket.get_by_tg_id(session=session, tg_id=tg_id)

        if ticket:
            await session.execute(
                update(SupportTicket).where(SupportTicket.tg_id == tg_id).values(**kwargs)
            )
            await session.commit()
            await session.refresh(ticket)
            logger.debug(f"Support ticket for user {tg_id} updated.")
            return ticket

        logger.warning(f"Support ticket for user {tg_id} not found in the database.")
        return None
