"""Dedicated acceptance bot; uses the same adapter and operator handlers as the main bot."""
import asyncio
import logging
import os
from pathlib import Path
from aiogram import Bot,Dispatcher
from aiogram.client.default import DefaultBotProperties
from aiogram.enums import ParseMode
from aiogram.fsm.storage.memory import MemoryStorage
from app.bot.filters import IsAdmin
from app.bot.routers.admin_tools.web_trial_handler import router
from app.bot.services.web_trial import WebTrialAdapter

async def run():
    operators={int(v) for v in os.environ['BOT_OPERATOR_IDS'].split(',')}
    IsAdmin.set_admins(sorted(operators))
    adapter=WebTrialAdapter(os.environ['WEB_TRIAL_API_URL'],Path(os.environ['WEB_TRIAL_API_TOKEN_FILE']).read_text().strip(),operators,os.environ['WEB_TRIAL_API_CA_FILE'])
    bot=Bot(Path(os.environ['BOT_TOKEN_FILE']).read_text().strip(),default=DefaultBotProperties(parse_mode=ParseMode.HTML))
    dp=Dispatcher(storage=MemoryStorage(),web_trial=adapter)
    dp.include_router(router)
    @dp.errors()
    async def safe_error(event):
        logging.warning('test bot handler failed: SERVICE_UNAVAILABLE')
        return True
    try:
        # This bot is dedicated to acceptance, never the existing production bot.
        await bot.delete_webhook(drop_pending_updates=False)
        adapter.start(bot)
        await dp.start_polling(bot,close_bot_session=False)
    finally:
        await adapter.close()
        await bot.session.close()

if __name__=='__main__':
    logging.basicConfig(level=logging.WARNING)
    try:asyncio.run(run())
    except Exception:logging.error('test bot stopped: SERVICE_UNAVAILABLE');raise SystemExit(1)
