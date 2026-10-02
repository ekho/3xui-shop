import asyncio
import json
import ssl
import subprocess
import tempfile
import unittest
from datetime import datetime, timedelta, timezone
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import AsyncMock
from aiogram.exceptions import TelegramBadRequest
from aiogram.methods import EditMessageText
from uuid import uuid4
from aiohttp import web
from app.bot.services.web_trial import WebTrialAdapter, WebTrialAPIError, render_card

class WebTrialAdapterTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        cert, key = Path(self.tmp.name)/"cert.pem", Path(self.tmp.name)/"key.pem"
        subprocess.run(["openssl","req","-x509","-newkey","rsa:2048","-nodes","-keyout",str(key),"-out",str(cert),"-days","1","-subj","/CN=localhost","-addext","subjectAltName=IP:127.0.0.1"], check=True, capture_output=True)
        self.cert = str(cert)
        ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        ctx.load_cert_chain(cert, key)
        self.calls, self.status, self.claims = [], 200, []
        self.job = {"job_id":str(uuid4()),"chat_id":101,"kind":"approval_card","lease_token":"x"*43,"lease_expires_at":(datetime.now(timezone.utc)+timedelta(seconds=60)).isoformat(),"payload":{"request_id":str(uuid4()),"operation_id":None,"target_message_id":None,"email":"client@example.test","comment":"<b>unsafe</b>","created_at":datetime.now(timezone.utc).isoformat(),"status":"pending"}}
        self.operation = str(uuid4())
        async def handler(request):
            self.assertEqual(request.headers.get("Authorization"),"Bearer fixture-token")
            body = await request.json()
            self.calls.append((request.path,body,dict(request.headers)))
            if self.status != 200:
                return web.json_response({"error":{"code":"INVALID_CREDENTIALS","message":"safe","request_id":str(uuid4())}},status=self.status)
            if request.path.endswith("/claim"):
                return web.json_response({"jobs":self.claims})
            if request.path.endswith("/decision"):
                return web.json_response({"request":{"request_id":body.get("request_id",self.job["payload"]["request_id"]),"status":"approved","created_at":datetime.now(timezone.utc).isoformat(),"decided_at":datetime.now(timezone.utc).isoformat(),"operation_id":self.operation,"previous_request_id":None},"operation_id":self.operation,"card":dict(self.job["payload"],status="approved",operation_id=self.operation),"delivery_state":"pending"})
            if request.path.endswith("/reconsider"):
                return web.json_response({"request_id":self.operation,"previous_request_id":request.path.split("/")[-2],"status":"pending"},status=201)
            if request.path.endswith("/reconcile"):
                return web.json_response({"operation_id":request.path.split("/")[-2],"status":"provisioning"},status=202)
            if request.path.endswith("/result"):self.claims=[]
            return web.json_response({})
        app = web.Application();app.router.add_post("/{tail:.*}",handler)
        self.runner=web.AppRunner(app);await self.runner.setup();site=web.TCPSite(self.runner,"127.0.0.1",0,ssl_context=ctx);await site.start()
        port=site._server.sockets[0].getsockname()[1]
        self.adapter=WebTrialAdapter(f"https://127.0.0.1:{port}","fixture-token",{101,202},self.cert)
        self.addAsyncCleanup(self.adapter.close);self.addAsyncCleanup(self.runner.cleanup)

    async def test_lost_ack_and_actor(self):
        self.claims=[self.job];j=await self.adapter.claim();self.assertEqual(j["job_id"],self.job["job_id"]);self.assertEqual(self.calls[0][1],{"limit":1})
        text,keyboard=render_card(j["payload"]);self.assertIn("&lt;b&gt;unsafe&lt;/b&gt;",text)
        for row in keyboard.inline_keyboard:
            for button in row:self.assertLessEqual(len(button.callback_data.encode()),64)
        a=await self.adapter.decide(j["payload"]["request_id"],101,"approve","first-callback")
        b=await self.adapter.decide(j["payload"]["request_id"],101,"approve","lost-reply-retry")
        self.assertEqual(a["operation_id"],b["operation_id"]);self.assertEqual(self.calls[-1][1]["operator_tg_id"],101)
        await self.adapter.complete(j,{"kind":"sent","chat_id":101,"message_id":11})
        self.assertEqual(self.calls[-1][1]["lease_token"],j["lease_token"])

    async def test_delivery_replaces_deleted_review_card(self):
        self.claims=[self.job]
        message=SimpleNamespace(chat=SimpleNamespace(id=101),message_id=11)
        bot=SimpleNamespace(send_message=AsyncMock(return_value=message),edit_message_text=AsyncMock())
        self.adapter.start(bot)
        for _ in range(100):
            if any(path.endswith("/result") for path,_,_ in self.calls):break
            await asyncio.sleep(.01)
        await self.adapter.close()
        sent=[body for path,body,_ in self.calls if path.endswith("/result")]
        self.assertTrue(sent);self.assertEqual(sent[0]["result"],{"kind":"sent","chat_id":101,"message_id":11})
        bot.send_message.assert_awaited()
        # A separate adapter instance models the next bot process / fresh lease.
        self.calls.clear();self.job["payload"].update(target_message_id=11,status="needs_review",operation_id=self.operation);self.claims=[self.job]
        self.adapter=WebTrialAdapter(self.adapter.url,"fixture-token",{101,202},self.cert)
        self.addAsyncCleanup(self.adapter.close)
        bot.send_message.reset_mock()
        bot.send_message.return_value=SimpleNamespace(chat=SimpleNamespace(id=101),message_id=22)
        bot.edit_message_text.side_effect=TelegramBadRequest(EditMessageText(text="fixture",chat_id=101,message_id=11),"Bad Request: message to edit not found")
        self.adapter.start(bot)
        for _ in range(100):
            if any(path.endswith("/result") for path,_,_ in self.calls):break
            await asyncio.sleep(.01)
        await self.adapter.close()
        sent=[body for path,body,_ in self.calls if path.endswith("/result")]
        self.assertTrue(sent);self.assertEqual(sent[0]["result"],{"kind":"sent","chat_id":101,"message_id":22})
        bot.send_message.assert_awaited_once()
        self.assertTrue(bot.send_message.await_args.kwargs["reply_markup"].inline_keyboard[0][0].callback_data.startswith("wt1:c:"))

    async def test_unchanged_card_acks_without_replacement(self):
        self.job["payload"]["target_message_id"]=11;self.claims=[self.job]
        bot=SimpleNamespace(send_message=AsyncMock(),edit_message_text=AsyncMock(side_effect=TelegramBadRequest(EditMessageText(text="fixture",chat_id=101,message_id=11),"Bad Request: message is not modified: specified content is identical")))
        self.adapter.start(bot)
        for _ in range(100):
            if any(path.endswith("/result") for path,_,_ in self.calls):break
            await asyncio.sleep(.01)
        await self.adapter.close()
        sent=[body for path,body,_ in self.calls if path.endswith("/result")]
        self.assertEqual(sent[0]["result"],{"kind":"sent","chat_id":101,"message_id":11})
        bot.send_message.assert_not_awaited()

    async def test_replacement_timeout_leaves_lease_unacknowledged(self):
        self.job["payload"]["target_message_id"]=11;self.claims=[self.job]
        attempted=asyncio.Event()
        async def lost_reply(**kwargs):
            attempted.set()
            raise TimeoutError()
        bot=SimpleNamespace(send_message=AsyncMock(side_effect=lost_reply),edit_message_text=AsyncMock(side_effect=TelegramBadRequest(EditMessageText(text="fixture",chat_id=101,message_id=11),"Bad Request: message to edit not found")))
        self.adapter.start(bot)
        await asyncio.wait_for(attempted.wait(),2)
        await asyncio.sleep(0)
        await self.adapter.close()
        self.assertFalse(any(path.endswith("/result") for path,_,_ in self.calls))
        bot.send_message.assert_awaited_once()

    async def test_support_paths_and_key(self):
        key=str(uuid4());identifier=self.job["payload"]["request_id"]
        with self.assertRaises(WebTrialAPIError):await self.adapter.reconsider(identifier,101,"",key)
        new=await self.adapter.reconsider(identifier,101,"Support reason",key)
        self.assertEqual(new["previous_request_id"],identifier)
        self.assertEqual(self.calls[-1][2]["Idempotency-Key"],key)
        self.assertEqual(self.calls[-1][1],{"operator_tg_id":101,"reason":"Support reason"})
        recovered=await self.adapter.reconcile(self.operation,101,"Panel checked",key)
        self.assertEqual(recovered["operation_id"],self.operation)

    async def test_lifecycle(self):
        self.adapter.start(SimpleNamespace());first=self.adapter.task;self.adapter.start(SimpleNamespace());self.assertIs(first,self.adapter.task)
        for _ in range(50):
            if self.adapter.session is not None:break
            await asyncio.sleep(.01)
        session=self.adapter.session;self.assertIsNotNone(session)
        await self.adapter.close();self.assertTrue(first.done());self.assertTrue(session.closed)
        await self.adapter.close()

    async def test_permanent_error_stops_only_adapter(self):
        self.status=401;self.adapter.start(SimpleNamespace());task=self.adapter.task
        await asyncio.wait_for(task,2);self.assertTrue(self.adapter.disabled)
        with self.assertRaises(WebTrialAPIError):await self.adapter.claim()

    async def test_contract_error_and_tls(self):
        self.claims=[dict(self.job,chat_id=999)]
        with self.assertRaises(WebTrialAPIError):await self.adapter.claim()
        untrusted=WebTrialAdapter(self.adapter.url,"fixture-token",{101})
        self.addAsyncCleanup(untrusted.close)
        with self.assertRaises(WebTrialAPIError):await untrusted.claim()

    async def test_close_survives_failed_poller(self):
        async def fail():raise RuntimeError("controlled background fault")
        self.adapter.task=asyncio.create_task(fail())
        await asyncio.sleep(0)
        await self.adapter.close()
        self.assertTrue(self.adapter.disabled)
    async def test_card_rejects_malformed_contract(self):
        p=dict(self.job["payload"]);p.pop("comment")
        with self.assertRaises(WebTrialAPIError):render_card(p)

    async def test_telegram_only_card_has_real_identity_without_email(self):
        payload = dict(self.job["payload"], email=None, display_name="<Support User>", telegram_id="9223372036854775807")
        text, keyboard = render_card(payload)
        self.assertIn("&lt;Support User&gt;", text)
        self.assertIn("9223372036854775807", text)
        self.assertNotIn("Email: None", text)
        self.assertIsNotNone(keyboard)
        for bad in ("0", "-1", "9223372036854775808", "1.0"):
            with self.assertRaises(WebTrialAPIError):
                render_card(dict(payload, telegram_id=bad))


if __name__ == "__main__":unittest.main()

class WebTrialConfigTests(unittest.TestCase):
    def test_file_only_configuration(self):
        from environs import Env
        from unittest.mock import patch
        import os
        from app.config import web_trial_settings
        with tempfile.TemporaryDirectory() as directory, patch.dict(os.environ,{},clear=True):
            env=Env();self.assertEqual(web_trial_settings(env),(None,None,None))
            os.environ["WEB_TRIAL_API_URL"]="https://backend.example.test"
            with self.assertRaises(ValueError):web_trial_settings(env)
            token=Path(directory)/"token";token.write_text("fixture-token")
            os.environ["WEB_TRIAL_API_TOKEN_FILE"]=str(token)
            self.assertEqual(web_trial_settings(env)[:2],("https://backend.example.test","fixture-token"))
            os.environ["WEB_TRIAL_API_TOKEN"]="plaintext-forbidden"
            with self.assertRaises(ValueError):web_trial_settings(env)
