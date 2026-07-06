import logging
from typing import Any, Self

from sqlalchemy import JSON, Integer, select, update
from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy.orm import Mapped, mapped_column

from app.bot.utils.constants import DEFAULT_INBOUND_GROUPS

from . import Base

logger = logging.getLogger(__name__)


class Plan(Base):
    """
    Represents a subscription plan (device tier) in the database.

    Attributes:
        id (int): Unique identifier (primary key).
        devices (int): Device (simultaneous connections) count — the plan's lookup key.
        traffic_gb (int): Traffic limit in GB, 0 = unlimited.
        prices (dict): {currency_code: {duration_days_as_str: price}}, e.g. {"RUB": {"30": 70.0}}.
    """

    __tablename__ = "plans"

    id: Mapped[int] = mapped_column(primary_key=True, autoincrement=True)
    devices: Mapped[int] = mapped_column(Integer, unique=True, nullable=False)
    traffic_gb: Mapped[int] = mapped_column(Integer, default=0, nullable=False)
    prices: Mapped[dict[str, Any]] = mapped_column(JSON, nullable=False)
    # Набор групп инбаундов, который получает купивший этот тариф (JSON-массив имён).
    inbound_groups: Mapped[list[str]] = mapped_column(
        JSON, default=lambda: list(DEFAULT_INBOUND_GROUPS), nullable=False
    )

    def __repr__(self) -> str:
        return f"<Plan(id={self.id}, devices={self.devices}, traffic_gb={self.traffic_gb})>"

    @classmethod
    async def get_all(cls, session: AsyncSession) -> list[Self]:
        query = await session.execute(select(Plan).order_by(Plan.devices))
        return query.scalars().all()

    @classmethod
    async def get_by_devices(cls, session: AsyncSession, devices: int) -> Self | None:
        filter = [Plan.devices == devices]
        query = await session.execute(select(Plan).where(*filter))
        return query.scalar_one_or_none()

    @classmethod
    async def create(cls, session: AsyncSession, devices: int, **kwargs: Any) -> Self | None:
        if await Plan.get_by_devices(session=session, devices=devices):
            logger.warning(f"Plan for {devices} devices already exists.")
            return None

        plan = Plan(devices=devices, **kwargs)
        session.add(plan)

        try:
            await session.commit()
            logger.info(f"Plan for {devices} devices created.")
            return plan
        except IntegrityError as exception:
            await session.rollback()
            logger.error(f"Error occurred while creating plan for {devices} devices: {exception}")
            return None

    @classmethod
    async def update(cls, session: AsyncSession, devices: int, **kwargs: Any) -> Self | None:
        plan = await Plan.get_by_devices(session=session, devices=devices)

        if not plan:
            logger.warning(f"Plan for {devices} devices not found for update.")
            return None

        filter = [Plan.devices == devices]
        await session.execute(update(Plan).where(*filter).values(**kwargs))
        await session.commit()
        logger.info(f"Plan for {devices} devices updated.")
        return await Plan.get_by_devices(session=session, devices=devices)

    @classmethod
    async def delete(cls, session: AsyncSession, devices: int) -> bool:
        plan = await Plan.get_by_devices(session=session, devices=devices)

        if plan:
            await session.delete(plan)
            await session.commit()
            logger.info(f"Plan for {devices} devices deleted.")
            return True

        logger.warning(f"Plan for {devices} devices not found for deletion.")
        return False

    @classmethod
    async def count(cls, session: AsyncSession) -> int:
        query = await session.execute(select(Plan))
        return len(query.scalars().all())
