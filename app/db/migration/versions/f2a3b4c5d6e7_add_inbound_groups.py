"""add inbound groups (registry table + user/plan columns)

Revision ID: f2a3b4c5d6e7
Revises: e1f2a3b4c5d6
Create Date: 2026-07-06 18:00:00.000000

Этап 6: наборы инбаундов. Группа = префикс тега инбаунда в панели; здесь —
реестр известных групп (бот управляет только ими), набор групп у юзера
(NULL = дефолтный ["regular"], reconciler доцепит) и набор групп у тарифа.
Сеем 'regular', чтобы система работала из коробки без ручного создания.
"""

from typing import Sequence, Union

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision: str = "f2a3b4c5d6e7"
down_revision: Union[str, None] = "e1f2a3b4c5d6"
branch_labels: Union[str, Sequence[str], None] = None
depends_on: Union[str, Sequence[str], None] = None


def upgrade() -> None:
    op.create_table(
        "inbound_groups",
        sa.Column("id", sa.Integer(), primary_key=True, autoincrement=True),
        sa.Column("name", sa.String(length=32), nullable=False),
        sa.Column("created_at", sa.DateTime(), nullable=False, server_default=sa.func.now()),
        sa.UniqueConstraint("name", name="uq_inbound_groups_name"),
    )
    op.execute("INSERT INTO inbound_groups (name) VALUES ('regular')")

    op.add_column("users", sa.Column("inbound_groups", sa.JSON(), nullable=True))
    # Существующие тарифы получают дефолтный набор — поведение не меняется до явной правки.
    op.add_column(
        "plans",
        sa.Column("inbound_groups", sa.JSON(), nullable=False, server_default='["regular"]'),
    )


def downgrade() -> None:
    op.drop_column("plans", "inbound_groups")
    op.drop_column("users", "inbound_groups")
    op.drop_table("inbound_groups")
