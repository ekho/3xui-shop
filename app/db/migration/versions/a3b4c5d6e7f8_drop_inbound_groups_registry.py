"""drop inbound_groups registry table

Revision ID: a3b4c5d6e7f8
Revises: f2a3b4c5d6e7
Create Date: 2026-07-06 20:00:00.000000

Пересмотр модели владения: список групп живёт ТОЛЬКО в панели 3x-ui (страница
Groups, таблица client_groups) — бот его синкает по API, а не ведёт свой реестр.
Локальная таблица inbound_groups (введена в f2a3b4c5d6e7) больше не нужна.
Колонки users.inbound_groups и plans.inbound_groups остаются — это связки
юзер/тариф<->группы, которыми управляет бот.
"""

from typing import Sequence, Union

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision: str = "a3b4c5d6e7f8"
down_revision: Union[str, None] = "f2a3b4c5d6e7"
branch_labels: Union[str, Sequence[str], None] = None
depends_on: Union[str, Sequence[str], None] = None


def upgrade() -> None:
    op.drop_table("inbound_groups")


def downgrade() -> None:
    op.create_table(
        "inbound_groups",
        sa.Column("id", sa.Integer(), primary_key=True, autoincrement=True),
        sa.Column("name", sa.String(length=32), nullable=False),
        sa.Column("created_at", sa.DateTime(), nullable=False, server_default=sa.func.now()),
        sa.UniqueConstraint("name", name="uq_inbound_groups_name"),
    )
    op.execute("INSERT INTO inbound_groups (name) VALUES ('regular')")
