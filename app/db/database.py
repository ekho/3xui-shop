import logging
from typing import Self

from sqlalchemy import event
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker, create_async_engine

from app.config import DatabaseConfig

from . import models

logger = logging.getLogger(__name__)


class Database:
    def __init__(self, config: DatabaseConfig) -> None:
        url = config.url()
        self.engine = create_async_engine(
            url=url,
            pool_pre_ping=True,
        )
        # M11: SQLite под конкурентной нагрузкой (вебхук + APScheduler-задачи в одном процессе).
        #      Без WAL/busy_timeout совпадение двух записей сразу даёт 'database is locked'
        #      на пути активации оплаты. Листенер применяется только для sqlite-URL.
        if url.startswith("sqlite"):

            @event.listens_for(self.engine.sync_engine, "connect")
            def _set_sqlite_pragmas(dbapi_connection, _connection_record) -> None:
                cursor = dbapi_connection.cursor()
                cursor.execute("PRAGMA journal_mode=WAL")
                cursor.execute("PRAGMA busy_timeout=5000")  # ждать до 5с вместо мгновенной ошибки
                cursor.execute("PRAGMA synchronous=NORMAL")
                cursor.close()

        self.session = async_sessionmaker(
            bind=self.engine,
            class_=AsyncSession,
            expire_on_commit=False,
        )
        logger.debug("Database engine and session maker initialized successfully.")

    async def initialize(self) -> Self:
        try:
            async with self.engine.begin() as connection:
                await connection.run_sync(models.Base.metadata.create_all)
            logger.debug("Database schema initialized successfully.")
        except Exception as exception:
            logger.error(f"Error initializing database schema: {exception}")
            raise
        return self

    async def close(self) -> None:
        try:
            await self.engine.dispose()
            logger.debug("Database engine closed successfully.")
        except Exception as exception:
            logger.error(f"Error closing database engine: {exception}")
            raise
