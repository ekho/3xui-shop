"""add server subscription_url

Revision ID: c9f0a1b2d3e4
Revises: b8e4f1c2d3a5
Create Date: 2026-07-05 12:00:00.000000

Базовый URL подписки берётся из настроек самой 3x-ui панели (subURI) при добавлении сервера.
Заменяет построение URL из XUI_SUBSCRIPTION_PORT/XUI_SUBSCRIPTION_PATH (удалены из конфига).
"""

from typing import Sequence, Union

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision: str = "c9f0a1b2d3e4"
down_revision: Union[str, None] = "b8e4f1c2d3a5"
branch_labels: Union[str, Sequence[str], None] = None
depends_on: Union[str, Sequence[str], None] = None


def upgrade() -> None:
    with op.batch_alter_table("servers") as batch_op:
        batch_op.add_column(sa.Column("subscription_url", sa.String(length=512), nullable=True))


def downgrade() -> None:
    with op.batch_alter_table("servers") as batch_op:
        batch_op.drop_column("subscription_url")
