FROM python:3.12-slim-bookworm

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONPATH=/ \
    PIP_NO_CACHE_DIR=1 \
    POETRY_VERSION=1.8.5 \
    POETRY_VIRTUALENVS_CREATE=false \
    POETRY_NO_INTERACTION=1

WORKDIR /

# Зависимости — отдельным слоём: пересобираются только при изменении pyproject.toml/poetry.lock.
# poetry.lock фиксирует все транзитивные версии (в т.ч. py3xui 0.7.0 для 3x-ui v3.1+).
# --only main: dev-групп нет; --no-root: у проекта package-mode = false (ставим только зависимости).
COPY pyproject.toml poetry.lock ./
RUN pip install "poetry==${POETRY_VERSION}" \
    && poetry install --only main --no-root

# Код приложения и entrypoint
COPY ./app /app
COPY docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod +x /docker-entrypoint.sh \
    && mkdir -p /app/data /app/logs

# Healthcheck: TCP-проверка порта бота (без внешних зависимостей).
HEALTHCHECK --interval=30s --timeout=5s --start-period=40s --retries=3 \
    CMD python -c "import socket,os,sys; p=int(os.getenv('BOT_PORT','8080')); s=socket.socket(); s.settimeout(3); sys.exit(s.connect_ex(('127.0.0.1', p)))"

# Entrypoint компилит локали, накатывает миграции и запускает бота (см. docker-entrypoint.sh).
ENTRYPOINT ["/docker-entrypoint.sh"]
