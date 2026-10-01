"""Thin web-trial transport. Decisions and VPN issuance belong to the backend."""
import asyncio
import html
import json
import logging
import re
import ssl
from datetime import datetime, timezone
from urllib.parse import urlsplit
from uuid import UUID

import aiohttp
from aiogram.exceptions import (
    TelegramBadRequest, TelegramForbiddenError, TelegramNetworkError,
    TelegramRetryAfter, TelegramServerError,
)
from aiogram.types import InlineKeyboardButton, InlineKeyboardMarkup

logger = logging.getLogger(__name__)
ERROR_CODES = {"INVALID_INPUT", "INVALID_CREDENTIALS", "EMAIL_VERIFICATION_REQUIRED", "ACCOUNT_RESTRICTED", "TRIAL_DISABLED", "TRIAL_ALREADY_USED", "TRIAL_RECONSIDERATION_REQUIRED", "REQUEST_STATE_CONFLICT", "OPERATION_NOT_READY", "IDEMPOTENCY_CONFLICT", "RATE_LIMITED", "SERVICE_UNAVAILABLE"}
STATUS_TEXT = {
    "pending": "Ожидает решения", "approved": "Одобрено. Выдача запускается",
    "rejected": "Отказано", "provisioning": "Выдача выполняется",
    "needs_review": "Нужна проверка поддержки", "active": "Доступ выдан",
    "expired": "Доступ истёк",
}

class WebTrialAPIError(Exception):
    def __init__(self, status: int, code: str, details: dict | None = None):
        self.status, self.code, self.details = status, code, details or {}
        super().__init__(code)

def valid_uuid(value):
    try:
        return isinstance(value, str) and UUID(value).int != 0
    except ValueError:
        return False

def valid_payload(p):
    fields = {"request_id", "operation_id", "target_message_id", "email", "comment", "created_at", "status"}
    return (isinstance(p, dict) and set(p) == fields and valid_uuid(p["request_id"])
            and isinstance(p["status"], str) and p["status"] in STATUS_TEXT
            and (p["operation_id"] is None or valid_uuid(p["operation_id"]))
            and (p["target_message_id"] is None or type(p["target_message_id"]) is int and p["target_message_id"] > 0)
            and all(isinstance(p[field], str) for field in ("email", "comment", "created_at"))
            and len(p["comment"]) <= 1000)

def render_card(payload):
    if not valid_payload(payload): raise WebTrialAPIError(400, "INVALID_RESPONSE")
    text = (f"<b>Web-триал</b>\nЗаявка: <code>{html.escape(payload['request_id'])}</code>\n"
            f"Email: {html.escape(payload['email'])}\n"
            f"Создана: {html.escape(payload['created_at'])}\n"
            f"Комментарий: {html.escape(payload['comment'])}\n\n"
            f"{STATUS_TEXT[payload['status']]}")
    request_id, operation_id = payload["request_id"], payload["operation_id"]
    rows = []
    if payload["status"] == "pending":
        rows = [[InlineKeyboardButton(text="Одобрить", callback_data=f"wt1:a:{request_id}"),
                 InlineKeyboardButton(text="Отказать", callback_data=f"wt1:r:{request_id}")]]
    elif payload["status"] == "rejected":
        rows = [[InlineKeyboardButton(text="Пересмотреть", callback_data=f"wt1:s:{request_id}")]]
    elif payload["status"] == "needs_review" and operation_id:
        rows = [[InlineKeyboardButton(text="Проверить выдачу", callback_data=f"wt1:c:{operation_id}")]]
    return text, InlineKeyboardMarkup(inline_keyboard=rows) if rows else None

class WebTrialAdapter:
    def __init__(self, url: str, token: str, operators: set[int], ca_file: str | None = None):
        parsed = urlsplit(url)
        if parsed.scheme != "https" or not parsed.netloc or parsed.username or parsed.password or parsed.query or parsed.fragment or parsed.path not in ("", "/"):
            raise ValueError("WEB_TRIAL_API_URL must be an HTTPS origin")
        if not token or not operators or any(type(actor) is not int or actor <= 0 for actor in operators):
            raise ValueError("Web trial adapter requires credentials and main-bot operators")
        self.url, self.token, self.operators = url.rstrip("/"), token, operators
        self.tls = ssl.create_default_context(cafile=ca_file)
        self.session: aiohttp.ClientSession | None = None
        self.task: asyncio.Task | None = None
        self.disabled = False

    async def _request(self, path, body, key=None):
        if self.disabled:
            raise WebTrialAPIError(503, "SERVICE_UNAVAILABLE")
        if self.session is None:
            self.session = aiohttp.ClientSession(timeout=aiohttp.ClientTimeout(total=10), connector=aiohttp.TCPConnector(ssl=self.tls))
        headers = {"Authorization": "Bearer " + self.token}
        if key is not None:
            if not valid_uuid(key): raise WebTrialAPIError(400, "INVALID_INPUT")
            headers["Idempotency-Key"] = key
        try:
            async with self.session.post(self.url + path, json=body, headers=headers, allow_redirects=False) as response:
                raw = bytearray()
                async for chunk in response.content.iter_chunked(4096):
                    raw.extend(chunk)
                    if len(raw) > 32768: raise WebTrialAPIError(400, "INVALID_RESPONSE")
                try: data = json.loads(raw)
                except (ValueError, UnicodeError): raise WebTrialAPIError(400, "INVALID_RESPONSE") from None
                if not isinstance(data, dict): raise WebTrialAPIError(400, "INVALID_RESPONSE")
                if response.status not in (200, 201, 202):
                    error = data.get("error", {})
                    code = error.get("code") if isinstance(error, dict) else None
                    if not isinstance(code, str) or code not in ERROR_CODES: code = "INVALID_RESPONSE"
                    details = {}
                    if response.status == 409 and code == "REQUEST_STATE_CONFLICT":
                        if error.get("current_request_status") in ("pending", "approved", "rejected"):
                            details["current_request_status"] = error["current_request_status"]
                        if valid_uuid(error.get("operation_id")): details["operation_id"] = error["operation_id"]
                    raise WebTrialAPIError(response.status, code, details)
                return data
        except (aiohttp.ClientError, TimeoutError, ssl.SSLError):
            raise WebTrialAPIError(503, "SERVICE_UNAVAILABLE") from None

    async def claim(self) -> dict | None:
        data = await self._request("/internal/v1/telegram/jobs/claim", {"limit": 1})
        jobs = data.get("jobs")
        if not isinstance(jobs, list) or len(jobs) > 1: raise WebTrialAPIError(400, "INVALID_RESPONSE")
        if not jobs: return None
        j = jobs[0]
        if not isinstance(j, dict) or not valid_uuid(j.get("job_id")) or type(j.get("chat_id")) is not int or j["chat_id"] not in self.operators or j.get("kind") not in ("approval_card", "request_decided", "provision_review", "provision_applied") or not isinstance(j.get("lease_token"), str) or not re.fullmatch(r"[A-Za-z0-9_-]{43}", j["lease_token"]):
            raise WebTrialAPIError(400, "INVALID_RESPONSE")
        try:
            expires = datetime.fromisoformat(j["lease_expires_at"])
            if expires.tzinfo is None: raise ValueError()
        except (ValueError, KeyError, TypeError): raise WebTrialAPIError(400, "INVALID_RESPONSE") from None
        p = j.get("payload")
        if not valid_payload(p): raise WebTrialAPIError(400, "INVALID_RESPONSE")
        return j

    async def complete(self, job: dict, result: dict) -> None:
        await self._request(f"/internal/v1/telegram/jobs/{job['job_id']}/result", {"lease_token": job["lease_token"], "result": result})

    async def decide(self, request_id: str, operator_tg_id: int, decision: str, callback_query_id: str) -> dict:
        if not valid_uuid(request_id) or operator_tg_id not in self.operators or decision not in ("approve", "reject"):
            raise WebTrialAPIError(403, "INVALID_CREDENTIALS")
        out = await self._request(f"/internal/v1/trial-requests/{request_id}/decision", {"operator_tg_id": operator_tg_id, "decision": decision, "callback_query_id": callback_query_id})
        request = out.get("request")
        expected = "approved" if decision == "approve" else "rejected"
        if (not isinstance(request, dict) or request.get("request_id") != request_id
                or request.get("status") != expected or not valid_payload(out.get("card"))
                or out["card"]["request_id"] != request_id or out["card"]["status"] != expected
                or request.get("operation_id") != out.get("operation_id")
                or expected == "approved" and not valid_uuid(out.get("operation_id"))
                or expected == "rejected" and out.get("operation_id") is not None):
            raise WebTrialAPIError(400, "INVALID_RESPONSE")
        return out

    async def reconsider(self, request_id: str, operator_tg_id: int, reason: str, idempotency_key: str) -> dict:
        out = await self._support("trial-requests", request_id, "reconsider", operator_tg_id, reason, idempotency_key)
        if not valid_uuid(out.get("request_id")) or out.get("previous_request_id") != request_id or out.get("status") != "pending":
            raise WebTrialAPIError(400, "INVALID_RESPONSE")
        return out

    async def reconcile(self, operation_id: str, operator_tg_id: int, reason: str, idempotency_key: str) -> dict:
        out = await self._support("trial-operations", operation_id, "reconcile", operator_tg_id, reason, idempotency_key)
        if out.get("operation_id") != operation_id or out.get("status") not in ("pending", "provisioning", "needs_review", "applied"):
            raise WebTrialAPIError(400, "INVALID_RESPONSE")
        return out

    async def _support(self, resource, identifier, action, actor, reason, key):
        if not valid_uuid(identifier) or actor not in self.operators or not isinstance(reason, str) or not reason.strip() or len(reason) > 1000:
            raise WebTrialAPIError(400, "INVALID_INPUT")
        return await self._request(f"/internal/v1/{resource}/{identifier}/{action}", {"operator_tg_id": actor, "reason": reason}, key)

    def start(self, bot) -> None:
        if self.task is None and not self.disabled:
            self.task = asyncio.create_task(self.run(bot), name="web-trial-delivery")
            self.task.add_done_callback(self._task_done)

    def _task_done(self, task):
        if not task.cancelled() and task.exception() is not None:
            self.disabled = True
            logger.error("Web trial poller stopped unexpectedly (code=SERVICE_UNAVAILABLE)")

    async def run(self, bot) -> None:
        delay = 1
        try:
            while not self.disabled:
                try:
                    j = await self.claim()
                    if j is None:
                        delay = 1
                        await asyncio.sleep(5)
                        continue
                    if datetime.fromisoformat(j["lease_expires_at"]) <= datetime.now(timezone.utc):
                        await asyncio.sleep(1)
                        continue
                    text, keyboard = render_card(j["payload"])
                    target = j["payload"]["target_message_id"]
                    try:
                        if target:
                            message = await asyncio.wait_for(bot.edit_message_text(text=text, chat_id=j["chat_id"], message_id=target, reply_markup=keyboard, request_timeout=10), 10)
                        else:
                            message = await asyncio.wait_for(bot.send_message(chat_id=j["chat_id"], text=text, reply_markup=keyboard, request_timeout=10), 10)
                        if message is True and target:
                            result = {"kind": "sent", "chat_id": j["chat_id"], "message_id": target}
                        elif getattr(getattr(message, "chat", None), "id", None) == j["chat_id"] and type(getattr(message, "message_id", None)) is int and message.message_id > 0:
                            result = {"kind": "sent", "chat_id": message.chat.id, "message_id": message.message_id}
                        else:
                            result = {"kind": "delivery_failed", "code": "invalid_response"}
                    except TelegramRetryAfter as error:
                        await asyncio.sleep(max(1, error.retry_after))
                        continue  # Lease expiry requeues the same card; no immediate resend.
                    except TelegramForbiddenError:
                        result = {"kind": "delivery_failed", "code": "forbidden"}
                    except TelegramBadRequest:
                        result = {"kind": "delivery_failed", "code": "edit_failed" if target else "invalid_response"}
                    except (TelegramNetworkError, TelegramServerError, TimeoutError):
                        # Send may have succeeded: leave the lease to expire rather than invent an ack.
                        await asyncio.sleep(delay)
                        delay = min(30, delay * 2)
                        continue
                    await self.complete(j, result)
                    delay = 1
                except WebTrialAPIError as error:
                    if error.status in (400, 401, 403):
                        self.disabled = True
                        logger.error("Web trial adapter stopped: check configuration/contract (code=%s)", error.code)
                        break
                    logger.warning("Web trial adapter request failed (code=%s)", error.code)
                    await asyncio.sleep(delay)
                    delay = min(30, delay * 2)
        finally:
            self.disabled = True
            if self.session is not None: await self.session.close()

    async def close(self) -> None:
        self.disabled = True
        if self.task is not None and self.task is not asyncio.current_task():
            if not self.task.done(): self.task.cancel()
            try: await self.task
            except asyncio.CancelledError: pass
            except Exception: pass
        if self.session is not None: await self.session.close()
