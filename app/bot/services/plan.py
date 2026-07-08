import json
import logging
import os

from sqlalchemy.ext.asyncio import async_sessionmaker

from app.bot.models import Plan
from app.bot.utils.constants import (
    UNLIMITED_INBOUND_GROUP,
    UNLIMITED_PLAN_DEVICES,
    UNLIMITED_PLAN_TRAFFIC_GB,
)
from app.config import DEFAULT_PLANS_DIR
from app.db.models import Plan as PlanModel
from app.db.models import PlanDuration

logger = logging.getLogger(__name__)


class PlanService:
    """Тарифы (devices/traffic_gb/prices) и сроки подписки хранятся в БД (таблицы `plans`,
    `plan_durations`) и редактируются через Admin Tools -> Plans Editor — без передеплоя.
    Экземпляр держит прочитанный из БД снимок в памяти; после любой правки нужно вызвать
    load() заново (как ServerPoolService.sync_servers() для серверов).
    """

    def __init__(self, session_factory: async_sessionmaker) -> None:
        self.session_factory = session_factory
        self._plans: list[Plan] = []
        self._durations: list[int] = []
        logger.info("Plan Service initialized.")

    async def load(self) -> None:
        async with self.session_factory() as session:
            db_plans = await PlanModel.get_all(session)
            db_durations = await PlanDuration.get_all_sorted(session)

        if not db_plans and not db_durations:
            await self._bootstrap_from_legacy_json()
            async with self.session_factory() as session:
                db_plans = await PlanModel.get_all(session)
                db_durations = await PlanDuration.get_all_sorted(session)

        # Досеять скрытый безлимит-план, если его ещё нет (после bootstrap, чтобы не
        # сделать таблицу непустой раньше времени). Идемпотентно; правки админа не трёт.
        if await self._ensure_unlimited_plan():
            async with self.session_factory() as session:
                db_plans = await PlanModel.get_all(session)

        self._plans = [
            Plan.from_dict(
                {
                    "devices": p.devices,
                    "traffic_gb": p.traffic_gb,
                    "inbound_groups": p.inbound_groups,
                    "hidden": p.hidden,
                    "prices": p.prices,
                }
            )
            for p in db_plans
        ]
        self._durations = db_durations
        logger.info(f"Plans loaded: {len(self._plans)} plan(s), durations: {self._durations}.")

    async def _bootstrap_from_legacy_json(self) -> None:
        """Разовый перенос plans.json в БД, если таблицы ещё пустые (первый запуск на этой
        версии). Дальше файл больше не читается — источник истины БД."""
        if not os.path.isfile(DEFAULT_PLANS_DIR):
            logger.warning(
                f"No plans in the database and '{DEFAULT_PLANS_DIR}' not found — "
                "starting with zero plans. Add one via Admin Tools -> Plans Editor."
            )
            return

        try:
            with open(DEFAULT_PLANS_DIR, "r") as file:
                data = json.load(file)
        except (OSError, json.JSONDecodeError) as exception:
            logger.error(f"Failed to read legacy plans file '{DEFAULT_PLANS_DIR}': {exception}")
            return

        logger.info(f"Bootstrapping plans/durations from legacy '{DEFAULT_PLANS_DIR}' into the database.")
        async with self.session_factory() as session:
            for duration in data.get("durations", []):
                await PlanDuration.create(session=session, days=int(duration))
            for plan_data in data.get("plans", []):
                kwargs = {
                    "traffic_gb": plan_data.get("traffic_gb", 0),
                    "prices": plan_data["prices"],
                }
                # Опциональное поле: без него колонка получит дефолт ["regular"].
                if plan_data.get("inbound_groups"):
                    kwargs["inbound_groups"] = list(plan_data["inbound_groups"])
                await PlanModel.create(
                    session=session,
                    devices=plan_data["devices"],
                    **kwargs,
                )

    async def _ensure_unlimited_plan(self) -> bool:
        """Создать скрытый безлимит-план, если его нет. Идемпотентно; возвращает True,
        если создали (нужно перечитать). Опознаём по группе UNLIMITED_INBOUND_GROUP —
        так правки девайсов/трафика админом не приводят к дублю. Если слот devices
        уже занят обычным тарифом, не создаём (grant_unlimited тогда честно откажет)."""
        async with self.session_factory() as session:
            plans = await PlanModel.get_all(session)
            if any(UNLIMITED_INBOUND_GROUP in (p.inbound_groups or []) for p in plans):
                return False
            created = await PlanModel.create(
                session=session,
                devices=UNLIMITED_PLAN_DEVICES,
                traffic_gb=UNLIMITED_PLAN_TRAFFIC_GB,
                prices={},
                inbound_groups=[UNLIMITED_INBOUND_GROUP],
                hidden=True,
            )
        if created is None:
            logger.warning(
                f"Unlimited plan not seeded: a plan for {UNLIMITED_PLAN_DEVICES} devices "
                "already exists. Free that device count or edit the plan manually."
            )
            return False
        logger.info(
            f"Seeded hidden unlimited plan: {UNLIMITED_PLAN_DEVICES} devices / "
            f"{UNLIMITED_PLAN_TRAFFIC_GB}GB / group '{UNLIMITED_INBOUND_GROUP}'."
        )
        return True

    def get_plan(self, devices: int) -> Plan | None:
        plan = next((plan for plan in self._plans if plan.devices == devices), None)

        if not plan:
            logger.critical(f"Plan with {devices} devices not found.")

        return plan

    def get_all_plans(self) -> list[Plan]:
        return self._plans

    def get_purchasable_plans(self) -> list[Plan]:
        """Тарифы для меню покупки — без скрытых (напр. безлимит-план админа)."""
        return [plan for plan in self._plans if not plan.hidden]

    def get_unlimited_plan(self) -> Plan | None:
        """Скрытый безлимит-план — опознаётся по группе UNLIMITED_INBOUND_GROUP
        в наборе (та же группа, что тогглит админ). None, если он не заведён."""
        return next(
            (plan for plan in self._plans if UNLIMITED_INBOUND_GROUP in plan.inbound_groups),
            None,
        )

    def get_durations(self) -> list[int]:
        return self._durations
