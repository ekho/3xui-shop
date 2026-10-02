"""Real backend consumer. Without a fixture URL, starts the isolated Go fixture."""
import os
import subprocess
import unittest
from pathlib import Path
from urllib.parse import urlsplit
from uuid import UUID
from unittest.mock import AsyncMock, patch
from aiogram.types import Update, CallbackQuery, Message
from aiogram.fsm.context import FSMContext
from aiogram.fsm.storage.memory import MemoryStorage
from aiogram.fsm.storage.base import StorageKey
from app.bot.filters import IsAdmin
from app.bot.filters.is_dev import IsDev
from app.bot.routers.admin_tools.web_trial_handler import callback_web_trial
from app.bot.services.web_trial import WebTrialAdapter, WebTrialAPIError

class WebTrialContractTests(unittest.IsolatedAsyncioTestCase):
    async def test_real_consumer(self):
        url=os.environ.get('TRIAL_CONTRACT_URL')
        root=Path(__file__).resolve().parents[1]
        if not url:
            result=subprocess.run(['go','test','-C',str(root/'backend'),'./tests','-run','^TestWebTrialFlowAndFailures$','-count=1'],cwd=root,capture_output=True,text=True,timeout=120)
            self.assertEqual(result.returncode,0,result.stdout+result.stderr)
            return
        self.assertIn(urlsplit(url).hostname,('127.0.0.1','localhost'))
        request_id=str(UUID(os.environ['TRIAL_CONTRACT_REQUEST_ID']))
        token=Path(os.environ['TRIAL_CONTRACT_TOKEN_FILE']).read_text().strip()
        adapter=WebTrialAdapter(url,token,{101},os.environ['TRIAL_CONTRACT_CA_FILE'])
        self.addAsyncCleanup(adapter.close)
        IsAdmin.set_admins([101]);IsDev.set_developer(202)
        storage=MemoryStorage();self.addAsyncCleanup(storage.close)
        state=FSMContext(storage,StorageKey(bot_id=1,chat_id=101,user_id=101))
        found=False
        for i in range(20):
            job=await adapter.claim()
            if not job:break
            ack={'kind':'sent','chat_id':101,'message_id':i+1}
            await adapter.complete(job,ack);await adapter.complete(job,ack)
            if job['payload']['request_id']==request_id:
                self.assertEqual(job['kind'],'approval_card');found=True;break
        self.assertTrue(found,'durable card missing after bot downtime')
        update=Update.model_validate({'update_id':1,'callback_query':{'id':'fixture-'+request_id,'from':{'id':101,'is_bot':False,'first_name':'Operator'},'chat_instance':'fixture','data':'wt1:a:'+request_id,'message':{'message_id':1,'date':1790812800,'chat':{'id':101,'type':'private'},'text':'fixture card'}}})
        with patch.object(CallbackQuery,'answer',new_callable=AsyncMock) as answered,patch.object(Message,'edit_text',new_callable=AsyncMock),patch('app.bot.services.approval.ApprovalService.apply_decision',new_callable=AsyncMock) as legacy:
            await callback_web_trial(update.callback_query,state,adapter)
            await callback_web_trial(update.callback_query,state,adapter)
            self.assertEqual(answered.await_count,2)
            self.assertTrue(all('Решение принято' in a.args[0] for a in answered.await_args_list))
            legacy.assert_not_awaited()
        with self.assertRaises(WebTrialAPIError) as conflict:
            await adapter.decide(request_id,101,'reject','opposite-'+request_id)
        self.assertEqual(conflict.exception.status,409)
        self.assertEqual(conflict.exception.details['current_request_status'],'approved')
        with self.assertRaises(WebTrialAPIError) as actor:
            await adapter._request('/internal/v1/trial-requests/'+request_id+'/decision',{'operator_tg_id':999,'decision':'approve','callback_query_id':'forged'})
        self.assertEqual(actor.exception.status,403)
        with self.assertRaises(WebTrialAPIError) as unknown:
            await adapter._request('/internal/v1/telegram/jobs/claim',{'limit':1,'unknown':True})
        self.assertEqual(unknown.exception.status,400)

if __name__=='__main__':unittest.main()
