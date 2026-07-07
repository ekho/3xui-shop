"""add users.sub_id (subId как в 3x-ui, отделён от vpn_id)

Revision ID: d4e5f6a7b8c9
Revises: a3b4c5d6e7f8
Create Date: 2026-07-07 12:00:00.000000

Раньше один vpn_id (UUID v4) служил и креденшлом клиента (`id`), и subId страницы
подписки. Панель 3x-ui генерирует их раздельно: id=UUID v4, subId=randomLowerAndNum(16).
Заводим отдельную колонку users.sub_id под subId; ссылка подписки теперь строится по ней.

Backfill: существующим юзерам sub_id = vpn_id — их subId на панели сейчас равен их
UUID, поэтому ссылки продолжают работать без переезда клиентов. Новый 16-символьный
формат применяется только к вновь создаваемым пользователям (generate_sub_id()).
"""

from typing import Sequence, Union

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision: str = "d4e5f6a7b8c9"
down_revision: Union[str, None] = "a3b4c5d6e7f8"
branch_labels: Union[str, Sequence[str], None] = None
depends_on: Union[str, Sequence[str], None] = None


def upgrade() -> None:
    # 1) добавляем колонку nullable, чтобы прошёл backfill на существующих строках
    with op.batch_alter_table("users") as batch_op:
        batch_op.add_column(sa.Column("sub_id", sa.String(length=36), nullable=True))

    # 2) существующие юзеры: subId == текущий vpn_id (ссылки не ломаются)
    op.execute("UPDATE users SET sub_id = vpn_id WHERE sub_id IS NULL")

    # 3) фиксируем инвариант: NOT NULL + UNIQUE (имя как у uq_users_vpn_id — op.f-конвенция)
    with op.batch_alter_table("users") as batch_op:
        batch_op.alter_column("sub_id", existing_type=sa.String(length=36), nullable=False)
        batch_op.create_unique_constraint(op.f("uq_users_sub_id"), ["sub_id"])


def downgrade() -> None:
    with op.batch_alter_table("users") as batch_op:
        batch_op.drop_constraint(op.f("uq_users_sub_id"), type_="unique")
        batch_op.drop_column("sub_id")
