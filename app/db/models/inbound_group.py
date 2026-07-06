import logging
from datetime import datetime
from typing import Self

from sqlalchemy import String, delete, func, select, update
from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy.orm import Mapped, mapped_column

from . import Base

logger = logging.getLogger(__name__)


class InboundGroup(Base):
    """Реестр известных групп инбаундов.

    Бот управляет ТОЛЬКО инбаундами, чей тег-префикс есть в этом реестре
    (инбаунды с «чужими» тегами вроде `n2-...` для бота невидимы — ручные
    правки админа в панели не трогаются). Состав группы живёт в панели
    (теги инбаундов), здесь — лишь список имён.
    """

    __tablename__ = "inbound_groups"

    id: Mapped[int] = mapped_column(primary_key=True, autoincrement=True)
    name: Mapped[str] = mapped_column(String(32), unique=True, nullable=False)
    created_at: Mapped[datetime] = mapped_column(default=func.now(), nullable=False)

    def __repr__(self) -> str:
        return f"<InboundGroup(id={self.id}, name='{self.name}')>"

    @classmethod
    async def get_all(cls, session: AsyncSession) -> list[Self]:
        query = await session.execute(select(InboundGroup).order_by(InboundGroup.name))
        return query.scalars().all()

    @classmethod
    async def get_by_name(cls, session: AsyncSession, name: str) -> Self | None:
        query = await session.execute(select(InboundGroup).where(InboundGroup.name == name))
        return query.scalar_one_or_none()

    @classmethod
    async def create(cls, session: AsyncSession, name: str) -> Self | None:
        if await InboundGroup.get_by_name(session=session, name=name):
            logger.warning(f"Inbound group '{name}' already exists.")
            return None

        group = InboundGroup(name=name)
        session.add(group)

        try:
            await session.commit()
            logger.info(f"Inbound group '{name}' created.")
            return group
        except IntegrityError as exception:
            await session.rollback()
            logger.error(f"Error occurred while creating inbound group '{name}': {exception}")
            return None

    @classmethod
    async def rename(cls, session: AsyncSession, old_name: str, new_name: str) -> bool:
        group = await InboundGroup.get_by_name(session=session, name=old_name)

        if not group:
            logger.warning(f"Inbound group '{old_name}' not found for rename.")
            return False

        await session.execute(
            update(InboundGroup).where(InboundGroup.name == old_name).values(name=new_name)
        )
        await session.commit()
        logger.info(f"Inbound group '{old_name}' renamed to '{new_name}'.")
        return True

    @classmethod
    async def delete_by_name(cls, session: AsyncSession, name: str) -> bool:
        group = await InboundGroup.get_by_name(session=session, name=name)

        if not group:
            logger.warning(f"Inbound group '{name}' not found for deletion.")
            return False

        await session.execute(delete(InboundGroup).where(InboundGroup.name == name))
        await session.commit()
        logger.info(f"Inbound group '{name}' deleted.")
        return True
