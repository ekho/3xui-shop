import logging
from datetime import datetime
from typing import Any, Optional, Self

from sqlalchemy import JSON
from sqlalchemy import Enum as SQLEnum
from sqlalchemy import ForeignKey, String, func, select, update
from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy.orm import Mapped, mapped_column, relationship, selectinload

from app.bot.utils.constants import DEFAULT_LANGUAGE, ApprovalStatus

from . import Base

logger = logging.getLogger(__name__)


class User(Base):
    """
    Represents a user in the database.

    Attributes:
        id (int): Unique primary key for the user.
        tg_id (int): Unique Telegram user ID.
        vpn_id (str): Unique VPN identifier (client `id`/UUID credential on the panel).
        sub_id (str): Unique subscription identifier (panel `subId`; tail of the subscription URL).
        server_id (int | None): Foreign key referencing the server.
        first_name (str): First name of the user.
        last_name (str | None): Last name of the user.
        username (str | None): Telegram username of the user.
        created_at (datetime): Timestamp when the user was created.
        server (Server | None): Associated server object.
        transactions (list[Transaction]): List of transactions associated with the user.
        activated_promocodes (list[Promocode]): List of promocodes activated by the user.
        referrals_sent (list[Referral]): List of Referrals sent by the user and applied by referred users.
        referral (Referral | None): The Referral record if this user was invited.
    """

    __tablename__ = "users"

    id: Mapped[int] = mapped_column(primary_key=True, autoincrement=True)
    tg_id: Mapped[int] = mapped_column(unique=True, nullable=False)
    vpn_id: Mapped[str] = mapped_column(String(36), unique=True, nullable=False)
    # subId страницы подписки (как в 3x-ui). Отделён от vpn_id (=client id/UUID): у новых
    # юзеров генерируется через generate_sub_id() (16 симв. [0-9a-z]); у существующих
    # забэкфиллен значением vpn_id, чтобы их ссылки продолжали работать без переезда.
    sub_id: Mapped[str] = mapped_column(String(36), unique=True, nullable=False)
    server_id: Mapped[int | None] = mapped_column(
        ForeignKey("servers.id", ondelete="SET NULL"), nullable=True
    )
    first_name: Mapped[str] = mapped_column(String(length=32), nullable=False)
    # Telegram допускает до 64 символов; в SQLite длина не форсится, только документируется.
    last_name: Mapped[str | None] = mapped_column(String(length=64), nullable=True)
    username: Mapped[str | None] = mapped_column(String(length=32), nullable=True)
    language_code: Mapped[str] = mapped_column(
        String(length=5),
        nullable=False,
        default=DEFAULT_LANGUAGE,
    )
    created_at: Mapped[datetime] = mapped_column(default=func.now(), nullable=False)
    server: Mapped["Server | None"] = relationship("Server", back_populates="users", uselist=False)  # type: ignore
    transactions: Mapped[list["Transaction"]] = relationship("Transaction", back_populates="user")  # type: ignore
    activated_promocodes: Mapped[list["Promocode"]] = relationship(  # type: ignore
        "Promocode", back_populates="activated_user"
    )
    is_trial_used: Mapped[bool] = mapped_column(default=False, nullable=False)
    # G1: апрув-гейт. Хранить значение enum ("pending"/...) как в TransactionStatus (values_callable).
    approval_status: Mapped[ApprovalStatus] = mapped_column(
        SQLEnum(ApprovalStatus, values_callable=lambda obj: [e.value for e in obj]),
        default=ApprovalStatus.PENDING,
        server_default=ApprovalStatus.APPROVED.value,  # backfill существующих юзеров в approved
        nullable=False,
    )
    # M6: дедуп уведомления админам о заявке (не завязываемся на is_new_user)
    approval_requested_at: Mapped[datetime | None] = mapped_column(nullable=True)
    # Аудит решения по заявке: когда и кем (tg_id админа/оператора) одобрена/отклонена.
    # У юзеров, решённых до появления колонок, — NULL (карточка показывает «—»).
    approval_decided_at: Mapped[datetime | None] = mapped_column(nullable=True)
    approval_decided_by: Mapped[int | None] = mapped_column(nullable=True)
    # G4/Stars-рекуррент: charge_id ПЕРВОГО платежа подписки (B4 — его принимает
    # editUserStarSubscription), флаг активного автопродления, дата следующего списания (B5).
    stars_charge_id: Mapped[str | None] = mapped_column(String(64), nullable=True)
    is_stars_auto_renew: Mapped[bool] = mapped_column(default=False, nullable=False)
    stars_expires_at: Mapped[int | None] = mapped_column(nullable=True)  # subscription_expiration_date (unix, сек)
    # Набор групп инбаундов (JSON-массив имён). None -> дефолт DEFAULT_INBOUND_GROUPS;
    # заполняется при провижининге из тарифа, переопределяется админом (User Editor).
    inbound_groups: Mapped[list[str] | None] = mapped_column(JSON, nullable=True)
    referrals_sent: Mapped[list["Referral"]] = relationship(  # type: ignore
        "Referral",
        foreign_keys="Referral.referrer_tg_id",
        primaryjoin="User.tg_id == Referral.referrer_tg_id",
        back_populates="referrer",
        cascade="all, delete-orphan",
    )
    referral: Mapped["Referral | None"] = relationship(  # type: ignore
        "Referral",
        foreign_keys="Referral.referred_tg_id",
        primaryjoin="User.tg_id == Referral.referred_tg_id",
        back_populates="referred",
        uselist=False,
    )
    source_invite_name: Mapped[Optional[str]] = mapped_column(String(100), nullable=True)

    def __repr__(self) -> str:
        return (
            f"<User(id={self.id}, tg_id={self.tg_id}, vpn_id='{self.vpn_id}', "
            f"sub_id='{self.sub_id}', server_id={self.server_id}, first_name='{self.first_name}', "
            f"username='{self.username}', language_code='{self.language_code}', "
            f"created_at={self.created_at}, is_trial_used={self.is_trial_used})>"
        )

    @classmethod
    async def get(cls, session: AsyncSession, tg_id: int) -> Self | None:
        filter = [User.tg_id == tg_id]
        query = await session.execute(
            select(User)
            .options(
                selectinload(User.transactions),
                selectinload(User.activated_promocodes),
                selectinload(User.server),
            )
            .where(*filter)
        )
        user = query.scalar_one_or_none()

        if user:
            logger.debug(f"User {tg_id} retrieved from the database.")
            return user

        logger.debug(f"User {tg_id} not found in the database.")
        return None

    @classmethod
    async def get_all(cls, session: AsyncSession) -> list[Self]:
        query = await session.execute(select(User).options(selectinload(User.server)))
        return query.scalars().all()

    @classmethod
    async def create(cls, session: AsyncSession, tg_id: int, **kwargs: Any) -> Self | None:
        user = await User.get(session=session, tg_id=tg_id)

        if user:
            logger.warning(f"User {tg_id} already exists.")
            return None

        user = User(tg_id=tg_id, **kwargs)
        session.add(user)

        try:
            await session.commit()
            logger.debug(f"User {tg_id} created.")
            return user
        except IntegrityError as exception:
            await session.rollback()
            logger.error(f"Error occurred while creating user {tg_id}: {exception}")
            return None

    @classmethod
    async def update(cls, session: AsyncSession, tg_id: int, **kwargs: Any) -> Self | None:
        user = await User.get(session=session, tg_id=tg_id)

        if user:
            filter = [User.tg_id == tg_id]
            await session.execute(update(User).where(*filter).values(**kwargs))
            await session.commit()
            logger.debug(f"User {tg_id} updated.")
            return user

        logger.warning(f"User {tg_id} not found in the database.")
        return None

    @classmethod
    async def exists(cls, session: AsyncSession, tg_id: int) -> bool:
        return await User.get(session=session, tg_id=tg_id) is not None

    @classmethod
    async def update_trial_status(cls, session: AsyncSession, tg_id: int, used: bool) -> bool:
        """
        Updates the trial status of a user.

        Args:
            session (AsyncSession): Database session.
            tg_id (int): Telegram user ID.
            used (bool): Whether the trial has been used.

        Returns:
            bool: True if updated, False otherwise.
        """
        user = await cls.get(session=session, tg_id=tg_id)

        if not user:
            logger.warning(f"User {tg_id} not found to update trial status.")
            return False

        await session.execute(update(User).where(User.tg_id == tg_id).values(is_trial_used=used))
        await session.commit()
        logger.info(f"Trial status updated for user {tg_id}: {used}")
        return True
