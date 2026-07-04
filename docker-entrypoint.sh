#!/bin/sh
# Точка входа контейнера бота: локали → миграции → запуск.
# cd / — alembic.ini использует относительный script_location (app/db/migration) и prepend_sys_path=.
set -e
cd /

echo "[entrypoint] Compiling locales..."
# Локали некритичны: при сбое gettext откатится к ключам — не роняем старт из-за перевода.
pybabel compile -d /app/locales -D bot || echo "[entrypoint] WARN: pybabel compile failed; continuing."

echo "[entrypoint] Applying database migrations (alembic upgrade head)..."
alembic -c /app/db/alembic.ini upgrade head

echo "[entrypoint] Starting bot..."
exec python /app/__main__.py
