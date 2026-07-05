"""add plans and plan_durations tables

Revision ID: e1f2a3b4c5d6
Revises: c9f0a1b2d3e4
Create Date: 2026-07-06 12:00:00.000000

Тарифы (devices/traffic_gb/prices) и сроки подписки переезжают из статичного
plans.json в БД — редактируются через бота (Admin Tools -> Plans Editor), без
передеплоя. Таблицы создаются пустыми: при первом запуске PlanService сам
разово досеивает их из plans.json, если он смонтирован (см. plan.py).
"""

from typing import Sequence, Union

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision: str = "e1f2a3b4c5d6"
down_revision: Union[str, None] = "c9f0a1b2d3e4"
branch_labels: Union[str, Sequence[str], None] = None
depends_on: Union[str, Sequence[str], None] = None


def upgrade() -> None:
    op.create_table(
        "plans",
        sa.Column("id", sa.Integer(), primary_key=True, autoincrement=True),
        sa.Column("devices", sa.Integer(), nullable=False),
        sa.Column("traffic_gb", sa.Integer(), nullable=False),
        sa.Column("prices", sa.JSON(), nullable=False),
        sa.UniqueConstraint("devices", name="uq_plans_devices"),
    )
    op.create_table(
        "plan_durations",
        sa.Column("id", sa.Integer(), primary_key=True, autoincrement=True),
        sa.Column("days", sa.Integer(), nullable=False),
        sa.UniqueConstraint("days", name="uq_plan_durations_days"),
    )


def downgrade() -> None:
    op.drop_table("plan_durations")
    op.drop_table("plans")
