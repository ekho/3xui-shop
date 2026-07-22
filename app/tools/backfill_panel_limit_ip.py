"""Разовый бэкфилл limitIp в 3x-ui из БД бота.

Зачем:
  1) у части клиентов limitIp не проставился прежним багом — источник истины теперь
     БД бота, а не текущее значение в панели;
  2) вводится нахлёст «N устройств тарифа -> limitIp = N+1» на переключение сети
     (Wi-Fi↔LTE), см. app/bot/services/xui_clients.py и память xray-online-stats-api.

Как определяется N:
  из devices ПОСЛЕДНЕЙ completed-транзакции пользователя — единственное место в БД
  бота, где персистится купленный тариф (Transaction.subscription, упакованная
  SubscriptionData). Пользователи без сервера, без платных транзакций (триал/промокод/
  ручное заведение) и на безлимит-плане (is_unlimited) ПРОПУСКАЮТСЯ: их limitIp выставит
  сам провижининг при следующей операции.

Свойства:
  - идемпотентно: N берётся из БД (не из панели), повторный прогон даёт тот же результат;
  - точечно: меняется только limitIp, срок/трафик/членства не трогаются (VPNService.set_limit_ip);
  - dry-run по умолчанию — ничего не пишет, пока не передан --apply.

Запуск в контейнере бота:
    docker compose exec bot python -m app.tools.backfill_panel_limit_ip            # dry-run
    docker compose exec bot python -m app.tools.backfill_panel_limit_ip --apply    # применить
"""

from __future__ import annotations

import argparse
import asyncio
import logging

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.bot.models import SubscriptionData
from app.bot.services.inbound_groups import InboundGroupService
from app.bot.services.plan import PlanService
from app.bot.services.server_pool import ServerPoolService
from app.bot.services.vpn import VPNService
from app.bot.utils.constants import TransactionStatus
from app.bot.utils.py3xui_compat import apply_py3xui_patches
from app.config import load_config
from app.db.database import Database
from app.db.models import Transaction, User

logger = logging.getLogger("backfill_limit_ip")


async def resolve_plan_devices(session: AsyncSession, tg_id: int) -> int | None:
    """N устройств из последней completed-транзакции пользователя.

    Возвращает None, если платных транзакций с валидным devices нет (триал/промокод/
    ручное заведение) — таких клиентов бэкфилл пропускает.
    """
    rows = (
        (
            await session.execute(
                select(Transaction)
                .where(
                    Transaction.tg_id == tg_id,
                    Transaction.status == TransactionStatus.COMPLETED,
                )
                .order_by(Transaction.created_at.desc())
            )
        )
        .scalars()
        .all()
    )
    for tx in rows:
        try:
            devices = SubscriptionData.unpack(tx.subscription).devices
        except Exception:
            # Битая/устаревшая упаковка одной транзакции не должна ронять весь прогон.
            continue
        if devices and devices > 0:
            return devices
    return None


async def main(apply: bool) -> None:
    logging.basicConfig(level=logging.INFO, format="%(levelname)s %(message)s")
    config = load_config()
    apply_py3xui_patches()

    db = Database(config.database)
    await db.initialize()

    # Собираем только то, что нужно для записи клиента, без bot/i18n/redis (см. services.initialize).
    server_pool = ServerPoolService(config=config, session=db.session)
    plan = PlanService(session_factory=db.session)
    await plan.load()
    inbound_groups = InboundGroupService(session_factory=db.session)
    vpn = VPNService(
        config=config,
        session=db.session,
        server_pool_service=server_pool,
        plan_service=plan,
        inbound_group_service=inbound_groups,
    )
    await server_pool.sync_servers()

    async with db.session() as session:
        users = await User.get_all(session)

    stats = {"updated": 0, "no_server": 0, "no_tx": 0, "unlimited": 0, "failed": 0}
    for user in users:
        if not user.server_id:
            stats["no_server"] += 1
            continue
        if inbound_groups.is_unlimited(user):
            stats["unlimited"] += 1
            continue

        async with db.session() as session:
            devices = await resolve_plan_devices(session, user.tg_id)
        if not devices:
            stats["no_tx"] += 1
            continue

        if not apply:
            logger.info(f"[dry-run] {user.tg_id}: limitIp -> {devices} (panel {devices + 1})")
            stats["updated"] += 1
            continue

        if await vpn.set_limit_ip(user, devices):
            logger.info(f"{user.tg_id}: limitIp -> {devices} (panel {devices + 1})")
            stats["updated"] += 1
        else:
            logger.warning(f"{user.tg_id}: FAILED to set limitIp")
            stats["failed"] += 1

    mode = "APPLIED" if apply else "DRY-RUN (nothing written; pass --apply to write)"
    logger.info(f"Backfill {mode}. {stats}")
    await db.close()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Backfill 3x-ui limitIp (N+1) from bot DB.")
    parser.add_argument(
        "--apply", action="store_true", help="write changes to the panel (default: dry-run)"
    )
    args = parser.parse_args()
    asyncio.run(main(args.apply))
