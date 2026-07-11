"""add approval decision audit (decided_at/decided_by)

Revision ID: f4b5c6d7e8a9
Revises: c3d4e5f6a7b1
Create Date: 2026-07-11 12:00:00.000000

"""

from typing import Sequence, Union

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision: str = "f4b5c6d7e8a9"
down_revision: Union[str, None] = "c3d4e5f6a7b1"
branch_labels: Union[str, Sequence[str], None] = None
depends_on: Union[str, Sequence[str], None] = None


def upgrade() -> None:
    with op.batch_alter_table("users", schema=None) as batch_op:
        batch_op.add_column(sa.Column("approval_decided_at", sa.DateTime(), nullable=True))
        batch_op.add_column(sa.Column("approval_decided_by", sa.Integer(), nullable=True))


def downgrade() -> None:
    with op.batch_alter_table("users", schema=None) as batch_op:
        batch_op.drop_column("approval_decided_by")
        batch_op.drop_column("approval_decided_at")
