import unittest
from types import SimpleNamespace
from unittest.mock import AsyncMock, patch
from uuid import uuid4
from aiogram.fsm.context import FSMContext
from aiogram.fsm.storage.memory import MemoryStorage
from aiogram.fsm.storage.base import StorageKey
from app.bot.filters import IsAdmin
from app.bot.filters.is_dev import IsDev
from app.bot.services.web_trial import WebTrialAPIError
from app.bot.routers.admin_tools import web_trial_handler as handler

class WebTrialHandlerTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        IsAdmin.set_admins([101]);IsDev.set_developer(202)
        self.storage=MemoryStorage();self.addAsyncCleanup(self.storage.close)
        self.state=FSMContext(self.storage,StorageKey(bot_id=1,chat_id=101,user_id=101))
        self.id=str(uuid4());self.adapter=SimpleNamespace(disabled=False,decide=AsyncMock(return_value={"request":{"status":"approved"},"card":{"request_id":self.id,"operation_id":str(uuid4()),"email":"c@example.test","comment":"safe","created_at":"2026-10-01T00:00:00Z","status":"approved","target_message_id":None}}),reconsider=AsyncMock(),reconcile=AsyncMock())
        self.callback=SimpleNamespace(data="wt1:a:"+self.id,id="real-callback",from_user=SimpleNamespace(id=101,is_bot=False),message=SimpleNamespace(chat=SimpleNamespace(id=101,type="private"),edit_text=AsyncMock(),answer=AsyncMock()),answer=AsyncMock())
    async def test_lost_ack_and_actor(self):
        with patch("app.bot.services.approval.ApprovalService.apply_decision",new_callable=AsyncMock) as legacy:
            await handler.callback_web_trial(self.callback,self.state,self.adapter)
            self.adapter.decide.assert_awaited_once_with(self.id,101,"approve","real-callback");legacy.assert_not_awaited()
        self.callback.answer.assert_awaited_once();self.callback.message.edit_text.assert_not_awaited()
    async def test_delayed_decision_preserves_review_card(self):
        import asyncio
        from app.bot.services.web_trial import render_card
        entered, release = asyncio.Event(), asyncio.Event()
        response = self.adapter.decide.return_value
        async def delayed(*args):
            entered.set()
            await release.wait()
            return response
        self.adapter.decide.side_effect = delayed
        displayed = {}
        async def edit(text, reply_markup=None):
            displayed.update(text=text, keyboard=reply_markup)
        self.callback.message.edit_text.side_effect = edit
        decision = asyncio.create_task(handler.callback_web_trial(self.callback,self.state,self.adapter))
        await entered.wait()
        text, keyboard = render_card(dict(response["card"],status="needs_review"))
        await self.callback.message.edit_text(text,reply_markup=keyboard)
        release.set()
        await decision
        self.assertIn("Нужна проверка", displayed["text"])
        self.assertTrue(displayed["keyboard"].inline_keyboard[0][0].callback_data.startswith("wt1:c:"))
    async def test_actor_and_namespace_boundaries(self):
        for actor,is_bot,chat,namespace in [(999,False,"private","wt1:a:"),(101,True,"private","wt1:a:"),(101,False,"group","wt1:a:"),(101,False,"private","approval:")]:
            self.callback.from_user=SimpleNamespace(id=actor,is_bot=is_bot);self.callback.message.chat.type=chat;self.callback.data=namespace+self.id
            await handler.callback_web_trial(self.callback,self.state,self.adapter)
        self.adapter.decide.assert_not_awaited()
        self.callback.from_user=None;await handler.callback_web_trial(self.callback,self.state,self.adapter);self.adapter.decide.assert_not_awaited()
    async def test_backend_failure_is_visible(self):
        self.adapter.decide.side_effect=WebTrialAPIError(403,"INVALID_CREDENTIALS")
        await handler.callback_web_trial(self.callback,self.state,self.adapter)
        self.callback.answer.assert_awaited_once();self.assertTrue(self.callback.answer.await_args.kwargs["show_alert"]);self.callback.message.edit_text.assert_not_awaited()
    async def test_winning_state_is_not_false_approval(self):
        self.adapter.decide.side_effect=WebTrialAPIError(409,"REQUEST_STATE_CONFLICT",{"current_request_status":"rejected","operation_id":None})
        await handler.callback_web_trial(self.callback,self.state,self.adapter)
        self.assertIn("отказ",self.callback.answer.await_args.args[0].lower())

    async def test_reason_confirm_keeps_key_after_lost_reply(self):
        self.callback.data="wt1:s:"+self.id
        self.callback.message.edit_reply_markup=AsyncMock()
        await handler.callback_web_trial(self.callback,self.state,self.adapter)
        msg=SimpleNamespace(from_user=self.callback.from_user,chat=self.callback.message.chat,text="Support reviewed the request",answer=AsyncMock())
        await handler.message_web_trial_reason(msg,self.state,self.adapter)
        data=await self.state.get_data();key=data["web_trial_key"]
        self.callback.data="wt1:y:"+self.id;self.adapter.reconsider.side_effect=[WebTrialAPIError(503,"SERVICE_UNAVAILABLE"),{"request_id":str(uuid4())}]
        await handler.callback_web_trial(self.callback,self.state,self.adapter)
        self.assertEqual((await self.state.get_data())["web_trial_key"],key)
        await handler.callback_web_trial(self.callback,self.state,self.adapter)
        self.assertEqual(self.adapter.reconsider.await_count,2)
        self.assertEqual([a.args[-1] for a in self.adapter.reconsider.await_args_list],[key,key])
        self.assertIsNone(await self.state.get_state())


if __name__ == "__main__":unittest.main()

class WebTrialLifecycleTests(unittest.IsolatedAsyncioTestCase):
    async def test_acceptance_entrypoint_fresh_process(self):
        import subprocess
        import sys
        code = '''
import asyncio, os, tempfile
from pathlib import Path
from types import SimpleNamespace as NS
from unittest.mock import AsyncMock, patch
from deploy.s01 import bot_adapter as entry
from app.bot.routers.admin_tools import web_trial_handler as handler
async def check():
    identifier="11111111-1111-4111-8111-111111111111"
    adapter=NS(disabled=False, decide=AsyncMock(return_value={"request":{"status":"approved"},"card":{"request_id":identifier,"operation_id":identifier,"target_message_id":None,"email":"fixture@example.test","comment":"","created_at":"2026-10-01T00:00:00Z","status":"approved"}}), start=lambda bot:None, close=AsyncMock())
    bot=NS(delete_webhook=AsyncMock(),session=NS(close=AsyncMock()))
    async def polling(*args,**kwargs):
        for actor in (101,999):
            callback=NS(data="wt1:a:"+identifier,id="fixture",from_user=NS(id=actor,is_bot=False),message=NS(chat=NS(id=actor,type="private"),edit_text=AsyncMock()),answer=AsyncMock())
            await handler.callback_web_trial(callback,None,adapter)
        adapter.decide.assert_awaited_once_with(identifier,101,"approve","fixture")
    dispatcher=NS(include_router=lambda router:None,errors=lambda:lambda f:f,start_polling=polling)
    with tempfile.TemporaryDirectory() as folder:
        token=Path(folder)/"token";token.write_text("fixture")
        with patch.dict(os.environ,{"BOT_OPERATOR_IDS":"101","WEB_TRIAL_API_URL":"https://fixture.example.test","WEB_TRIAL_API_TOKEN_FILE":str(token),"WEB_TRIAL_API_CA_FILE":str(token),"BOT_TOKEN_FILE":str(token)}),patch.object(entry,"Bot",return_value=bot),patch.object(entry,"Dispatcher",return_value=dispatcher),patch.object(entry,"WebTrialAdapter",return_value=adapter):
            await entry.run()
asyncio.run(check())
'''
        result = subprocess.run([sys.executable, "-c", code], capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)

    async def test_lifecycle_both_bot_modes(self):
        from app import __main__ as main
        import asyncio
        adapter=SimpleNamespace(start=lambda bot:setattr(adapter,"starts",adapter.starts+1),close=AsyncMock(),starts=0)
        for mode in (True,False):
            config=SimpleNamespace(bot=SimpleNamespace(USE_WEBHOOK=mode))
            with patch.object(main,"_startup_bot",new_callable=AsyncMock) as startup:
                await main.on_startup(config=config,bot=object(),services=object(),db=object(),redis=object(),i18n=object(),web_trial=adapter)
                startup.assert_awaited_once()
        self.assertEqual(adapter.starts,2)
        with patch.object(main,"_startup_bot",new=AsyncMock(side_effect=RuntimeError("controlled startup fault"))):
            with self.assertRaises(RuntimeError):await main.on_startup(config=object(),bot=object(),services=object(),db=object(),redis=object(),i18n=object(),web_trial=adapter)
        self.assertEqual(adapter.starts,2)
        events=[]
        async def close_adapter():events.append("adapter")
        async def close_bot():events.append("bot")
        adapter.close.side_effect=close_adapter
        bot=SimpleNamespace(delete_webhook=AsyncMock(),session=SimpleNamespace(close=close_bot))
        services=SimpleNamespace(notification=SimpleNamespace(notify_developer=AsyncMock()))
        db=SimpleNamespace(close=AsyncMock())
        with patch.object(main.commands,"delete",new_callable=AsyncMock):await main.on_shutdown(db,bot,services,web_trial=adapter)
        self.assertEqual(events,["adapter","bot"])
