"""add support tickets (support proxy bot)

Revision ID: a9b8c7d6e5f4
Revises: e5f6a7b8c9d0
Create Date: 2026-07-08 13:30:00.000000

Прокси-поддержка: отдельный support-бот пересылает сообщения юзера в топик
супергруппы-форума и ответы админов обратно. Таблица хранит маппинг
"юзер ↔ топик" (один тикет на юзера) и статус (open/closed/banned).
thread_id NULL — топик ещё не создан (создаётся лениво при первом сообщении)
или был удалён вручную и будет пересоздан.
"""

from typing import Sequence, Union

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision: str = "a9b8c7d6e5f4"
down_revision: Union[str, None] = "e5f6a7b8c9d0"
branch_labels: Union[str, Sequence[str], None] = None
depends_on: Union[str, Sequence[str], None] = None


def upgrade() -> None:
    op.create_table(
        "support_tickets",
        sa.Column("id", sa.Integer(), primary_key=True, autoincrement=True),
        sa.Column("tg_id", sa.Integer(), nullable=False),
        sa.Column("thread_id", sa.Integer(), nullable=True),
        sa.Column(
            "status",
            sa.Enum("open", "closed", "banned", name="supportticketstatus"),
            nullable=False,
            server_default="open",
        ),
        sa.Column("created_at", sa.DateTime(), nullable=False, server_default=sa.func.now()),
        sa.Column("updated_at", sa.DateTime(), nullable=False, server_default=sa.func.now()),
        sa.UniqueConstraint("tg_id", name="uq_support_tickets_tg_id"),
    )


def downgrade() -> None:
    op.drop_table("support_tickets")
