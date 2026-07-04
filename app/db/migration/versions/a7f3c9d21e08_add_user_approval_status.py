"""add user approval_status (G1 approval gate)

Revision ID: a7f3c9d21e08
Revises: 032f2bef8d8d
Create Date: 2026-07-04 12:00:00.000000

Добавляет колонки апрув-гейта в users. Существующие юзеры бэкфилятся в 'approved'
(server_default), чтобы включение гейта не заблокировало текущих клиентов.
Новые юзеры создаются приложением со статусом 'pending' (модельный default).
"""

from typing import Sequence, Union

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision: str = "a7f3c9d21e08"
down_revision: Union[str, None] = "032f2bef8d8d"
branch_labels: Union[str, Sequence[str], None] = None
depends_on: Union[str, Sequence[str], None] = None


def upgrade() -> None:
    approval_enum = sa.Enum("pending", "approved", "rejected", name="approvalstatus")
    with op.batch_alter_table("users") as batch_op:
        batch_op.add_column(
            sa.Column(
                "approval_status",
                approval_enum,
                nullable=False,
                server_default="approved",  # backfill существующих строк
            )
        )
        batch_op.add_column(
            sa.Column("approval_requested_at", sa.DateTime(), nullable=True)
        )


def downgrade() -> None:
    with op.batch_alter_table("users") as batch_op:
        batch_op.drop_column("approval_requested_at")
        batch_op.drop_column("approval_status")
