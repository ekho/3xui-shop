"""Карточка пользователя для админских поверхностей.

Один рендерер на два бота: раздел «Пользователи» в админ-меню основного бота и
/info в топике support-бота. Оба диспетчера имеют i18n-middleware и общий
ServicesContainer, поэтому gettext-ключи работают в обоих контекстах.

`target` обязан быть загружен через User.get (selectinload transactions/
activated_promocodes/server) — иначе доступ к связям упадёт в async-контексте.
"""

import html
import logging
from datetime import datetime, timezone

from aiogram.utils.i18n import gettext as _
from sqlalchemy.ext.asyncio import AsyncSession

from app.bot.models import ClientData, ServicesContainer, SubscriptionData
from app.bot.utils.constants import ApprovalStatus, Currency, TransactionStatus
from app.bot.utils.formatting import format_size
from app.db.models import Referral, User

logger = logging.getLogger(__name__)

_TX_STATUS_ICONS = {
    TransactionStatus.COMPLETED: "✅",
    TransactionStatus.PENDING: "⏳",
    TransactionStatus.CANCELED: "❌",
    TransactionStatus.REFUNDED: "↩️",
}

_SHOWN_TX_COUNT = 3


def _format_date(value: datetime | None, with_time: bool = False) -> str:
    if not value:
        return "—"
    return value.strftime("%Y-%m-%d %H:%M" if with_time else "%Y-%m-%d")


def _format_expiry_date(expiry_time_ms: int) -> str:
    return datetime.fromtimestamp(expiry_time_ms / 1000, timezone.utc).strftime("%Y-%m-%d")


def _approval_line(target: User) -> str:
    decided_at = _format_date(target.approval_decided_at, with_time=True)
    if target.approval_status == ApprovalStatus.APPROVED:
        line = _("user_editor:card:approval_approved").format(date=decided_at)
    elif target.approval_status == ApprovalStatus.REJECTED:
        line = _("user_editor:card:approval_rejected").format(date=decided_at)
    else:
        line = _("user_editor:card:approval_pending").format(
            date=_format_date(target.approval_requested_at, with_time=True)
        )
    if target.approval_status != ApprovalStatus.PENDING and target.approval_decided_by:
        line += f" · <code>{target.approval_decided_by}</code>"
    return line


async def _source_line(target: User, session: AsyncSession) -> str:
    parts = []
    if target.source_invite_name:
        parts.append(
            _("user_editor:card:source_invite").format(
                name=html.escape(target.source_invite_name)
            )
        )
    referral = await Referral.get_referral(session=session, referred_tg_id=target.tg_id)
    if referral:
        referrer = referral.referrer
        referrer_label = (
            f"{html.escape(referrer.first_name)} (<code>{referrer.tg_id}</code>)"
            if referrer
            else f"<code>{referral.referrer_tg_id}</code>"
        )
        parts.append(_("user_editor:card:source_referral").format(referrer=referrer_label))
    return " · ".join(parts) if parts else "—"


def _subscription_block(target: User, client_data: ClientData | None) -> str:
    if not target.server_id:
        return _("user_editor:card:sub_none")
    if client_data is None:
        return _("user_editor:card:sub_error")

    if not client_data.enabled:
        status = _("user_editor:card:sub_status_disabled")
    elif client_data.has_subscription_expired:
        status = _("user_editor:card:sub_status_expired")
    else:
        status = _("user_editor:card:sub_status_active")

    if client_data._expiry_time == -1:
        expiry = client_data.expiry_time  # ∞
    elif client_data.has_subscription_expired:
        expiry = _("user_editor:card:sub_expired_at").format(
            date=_format_expiry_date(client_data._expiry_time)
        )
    else:
        expiry = _("user_editor:card:sub_expiry_active").format(
            remaining=client_data.expiry_time,
            date=_format_expiry_date(client_data._expiry_time),
        )

    traffic = client_data.traffic_used
    if client_data._traffic_total > 0:
        # Перерасход не показываем отрицательным остатком — клампим в 0.
        traffic += _("user_editor:card:traffic_limit").format(
            remaining=format_size(max(client_data._traffic_remaining, 0)),
            limit=client_data.traffic_total,
        )
        if client_data.has_traffic_exhausted:
            traffic += " ⚠️"

    return _("user_editor:card:sub_block").format(
        status=status,
        expiry=expiry,
        devices=client_data.max_devices,
        traffic=traffic,
    )


def _payments_summary(totals: dict[str, float]) -> str:
    if not totals:
        return "—"
    parts = []
    for code, amount in totals.items():
        try:
            symbol = Currency.from_code(code).symbol
        except ValueError:
            symbol = code
        parts.append(f"{amount:.2f} {symbol}")
    return ", ".join(parts)


def _transaction_lines(target: User, payment_method_currencies: dict[str, str]) -> str:
    transactions = sorted(target.transactions, key=lambda tx: tx.created_at, reverse=True)
    if not transactions:
        return ""

    lines = []
    for tx in transactions[:_SHOWN_TX_COUNT]:
        icon = _TX_STATUS_ICONS.get(tx.status, "•")
        date = tx.created_at.strftime("%Y-%m-%d")
        try:
            data = SubscriptionData.unpack(tx.subscription)
            method = data.state.value.removeprefix("pay_")
            symbol = ""
            for gateway_callback, code in payment_method_currencies.items():
                if gateway_callback in data.state.value:
                    symbol = Currency.from_code(code).symbol
                    break
            lines.append(f"• <code>{date}</code> {data.price:g} {symbol} · {method} {icon}")
        except Exception:
            # Старый/изменившийся формат packed-строки — показываем хотя бы дату и статус.
            lines.append(f"• <code>{date}</code> ? {icon}")

    return "\n" + "\n".join(lines)


async def build_user_card(
    target: User,
    session: AsyncSession,
    services: ServicesContainer,
    payment_method_currencies: dict[str, str] | None = None,
) -> tuple[str, ClientData | None]:
    """Собирает текст карточки. Возвращает (text, client_data) — live-данные панели
    отдаются вызывающему, чтобы он мог строить клавиатуру (кнопка сброса трафика)."""
    client_data = None
    if target.server_id:
        client_data = await services.vpn.get_client_data(target)

    payment_method_currencies = payment_method_currencies or {}
    totals = await services.payment_stats.get_user_payment_stats(
        user_id=target.tg_id,
        session=session,
        payment_method_currencies=payment_method_currencies,
    )

    groups = ", ".join(sorted(services.inbound_groups.effective_groups(target)))
    if services.inbound_groups.is_banned(target):
        groups += _("user_editor:card:banned_suffix")

    stars = "\n" + _("user_editor:card:stars_autorenew") if target.is_stars_auto_renew else ""

    text = _("user_editor:card:main").format(
        name=html.escape(target.first_name),
        username=f"@{target.username}" if target.username else "—",
        tg_id=target.tg_id,
        language=target.language_code,
        created_at=_format_date(target.created_at),
        source=await _source_line(target, session),
        approval=_approval_line(target),
        groups=groups,
        server=target.server.name if target.server else "—",
        trial=(
            _("user_editor:card:trial_yes")
            if target.is_trial_used
            else _("user_editor:card:trial_no")
        ),
        stars=stars,
        subscription=_subscription_block(target, client_data),
        payments=_payments_summary(totals),
        transactions=_transaction_lines(target, payment_method_currencies),
        referrals=await Referral.get_referral_count(session=session, referrer_tg_id=target.tg_id),
        promocodes=len(target.activated_promocodes),
    )
    return text, client_data
