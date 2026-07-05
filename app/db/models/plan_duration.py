import logging
from typing import Self

from sqlalchemy import Integer, select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy.orm import Mapped, mapped_column

from . import Base

logger = logging.getLogger(__name__)


class PlanDuration(Base):
    """
    Represents a subscription duration (in days) shared across every plan.

    Attributes:
        id (int): Unique identifier (primary key).
        days (int): Duration in days, unique.
    """

    __tablename__ = "plan_durations"

    id: Mapped[int] = mapped_column(primary_key=True, autoincrement=True)
    days: Mapped[int] = mapped_column(Integer, unique=True, nullable=False)

    def __repr__(self) -> str:
        return f"<PlanDuration(id={self.id}, days={self.days})>"

    @classmethod
    async def get_all_sorted(cls, session: AsyncSession) -> list[int]:
        query = await session.execute(select(PlanDuration.days).order_by(PlanDuration.days))
        return [row[0] for row in query.all()]

    @classmethod
    async def create(cls, session: AsyncSession, days: int) -> Self | None:
        duration = PlanDuration(days=days)
        session.add(duration)

        try:
            await session.commit()
            logger.info(f"Plan duration {days} days created.")
            return duration
        except IntegrityError as exception:
            await session.rollback()
            logger.error(f"Error occurred while creating plan duration {days}: {exception}")
            return None
