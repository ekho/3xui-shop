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
        self.callback.answer.assert_awaited_once();self.callback.message.edit_text.assert_awaited_once()
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
