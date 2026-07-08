"""add plans.hidden column

Revision ID: e5f6a7b8c9d0
Revises: d4e5f6a7b8c9
Create Date: 2026-07-08 12:00:00.000000

`hidden` — общий механизм «тариф вне меню покупки»: скрытый тариф нельзя купить,
он назначается только админом. На нём стоит безлимит-план (7 устройств, 100ГБ-кап,
бессрочно), выдаваемый переводом клиента в группу `unlimited` на экране User Groups.

Сам безлимит-план НЕ засевается здесь, а в коде (PlanService._ensure_unlimited_plan,
идемпотентно при старте) — чтобы не сделать таблицу plans непустой и не подавить
разовый bootstrap из legacy plans.json (см. plan.py: bootstrap срабатывает только
при пустой таблице).
"""

from typing import Sequence, Union

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision: str = "e5f6a7b8c9d0"
down_revision: Union[str, None] = "d4e5f6a7b8c9"
branch_labels: Union[str, Sequence[str], None] = None
depends_on: Union[str, Sequence[str], None] = None


def upgrade() -> None:
    op.add_column(
        "plans",
        sa.Column("hidden", sa.Boolean(), nullable=False, server_default=sa.false()),
    )


def downgrade() -> None:
    op.drop_column("plans", "hidden")
