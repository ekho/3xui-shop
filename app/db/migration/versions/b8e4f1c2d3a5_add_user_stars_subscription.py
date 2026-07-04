"""add user stars subscription fields (G4 Stars auto-renew)

Revision ID: b8e4f1c2d3a5
Revises: a7f3c9d21e08
Create Date: 2026-07-04 13:00:00.000000

Поля управления рекуррентной подпиской Telegram Stars:
- stars_charge_id: charge_id первого платежа подписки (B4 — его принимает editUserStarSubscription);
- is_stars_auto_renew: активно ли автопродление;
- stars_expires_at: дата следующего списания (subscription_expiration_date, unix-секунды, B5).
"""

from typing import Sequence, Union

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision: str = "b8e4f1c2d3a5"
down_revision: Union[str, None] = "a7f3c9d21e08"
branch_labels: Union[str, Sequence[str], None] = None
depends_on: Union[str, Sequence[str], None] = None


def upgrade() -> None:
    with op.batch_alter_table("users") as batch_op:
        batch_op.add_column(sa.Column("stars_charge_id", sa.String(length=64), nullable=True))
        batch_op.add_column(
            sa.Column(
                "is_stars_auto_renew",
                sa.Boolean(),
                nullable=False,
                server_default=sa.text("0"),  # существующие юзеры — без автопродления
            )
        )
        batch_op.add_column(sa.Column("stars_expires_at", sa.BigInteger(), nullable=True))


def downgrade() -> None:
    with op.batch_alter_table("users") as batch_op:
        batch_op.drop_column("stars_expires_at")
        batch_op.drop_column("is_stars_auto_renew")
        batch_op.drop_column("stars_charge_id")
