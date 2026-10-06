"""Disposable signed Cryptomus/Heleket API fixture. Never calls a provider."""
import base64
from datetime import datetime, timezone
from hashlib import md5, sha256
import hmac
from http.client import HTTPConnection
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import sqlite3
import ssl
import sys
import tempfile
import threading
import time
from uuid import UUID, uuid4


def sign(raw, key):
    return md5(base64.b64encode(raw) + key.encode(), usedforsecurity=False).hexdigest()


def initialize(path):
    with sqlite3.connect(path) as db:
        db.execute('CREATE TABLE IF NOT EXISTS payments (key TEXT PRIMARY KEY,id TEXT UNIQUE,request BLOB,payload TEXT,posts INTEGER DEFAULT 1,infos INTEGER DEFAULT 0)')
        db.execute('CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY,value INTEGER)')


class Stub(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass  # Auth, identifiers and payment bodies remain private.

    def reply(self, code, value):
        raw = json.dumps(value).encode()
        self.send_response(code)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def serve_request(self):
        try:
            length = int(self.headers.get('Content-Length', '0'))
            if not 0 <= length <= 16384:
                raise ValueError('body size')
            raw = self.rfile.read(length)
            if self.headers.get('merchant') != self.server.merchant or not hmac.compare_digest(self.headers.get('sign', ''), sign(raw, self.server.key)):
                return self.reply(401, {'state': 1})
            body = json.loads(raw.decode()) if raw else None
            with sqlite3.connect(self.server.db, timeout=10) as db:
                db.execute('BEGIN IMMEDIATE')
                if self.path == '/control':
                    if self.command == 'POST':
                        if body != {'fail_next_creation': True}:
                            raise ValueError('control')
                        db.execute("INSERT INTO settings VALUES('fail_next',1) ON CONFLICT(key) DO UPDATE SET value=1")
                        code, value = 200, {'armed': True}
                    else:
                        rows = db.execute('SELECT key,id,request,posts,infos,payload FROM payments ORDER BY key').fetchall()
                        code, value = 200, {'payments': [{'key': key, 'id': pid, 'request_sha256': sha256(request).hexdigest(), 'posts': posts, 'infos': infos, 'status': json.loads(payload)['status']} for key, pid, request, posts, infos, payload in rows]}
                elif self.path == '/v1/payment' and self.command == 'POST':
                    key = str(UUID(body['order_id']))
                    if body['currency'] != 'USD' or body['lifetime'] != 1800 or body['is_payment_multiple'] is not False or body.get('is_refresh', False):
                        raise ValueError('invoice')
                    row = db.execute('SELECT id,request,payload FROM payments WHERE key=?', (key,)).fetchone()
                    if row:
                        if row[1] != raw:
                            return self.reply(409, {'state': 1})
                        db.execute('UPDATE payments SET posts=posts+1 WHERE key=?', (key,))
                        code, value = 200, {'state': 0, 'result': json.loads(row[2])}
                    else:
                        pid = str(uuid4())
                        now = datetime.now(timezone.utc).isoformat()
                        payment = {'uuid': pid, 'order_id': key, 'amount': body['amount'], 'currency': 'USD',
                                   'status': 'check', 'payment_status': 'check', 'is_final': False,
                                   'payment_amount': None, 'payer_amount': None, 'merchant_amount': None, 'payer_currency': None,
                                   'created_at': now, 'updated_at': now, 'expired_at': int(time.time()) + 1800,
                                   'url': 'https://' + ('new-pay.heleket.com' if self.server.provider == 'heleket' else 'pay.cryptomus.com') + '/pay/' + pid}
                        db.execute('INSERT INTO payments(key,id,request,payload) VALUES(?,?,?,?)', (key, pid, raw, json.dumps(payment)))
                        fault = db.execute("SELECT value FROM settings WHERE key='fail_next'").fetchone()
                        code, value = (500 if fault and fault[0] else 200), {'state': 0, 'result': payment}
                        db.execute("DELETE FROM settings WHERE key='fail_next'")
                elif self.path == '/v1/payment/info' and self.command == 'POST' or self.path.startswith('/control/') and self.command == 'POST':
                    by_info = self.path == '/v1/payment/info'
                    key = str(UUID(body['order_id'])) if by_info else str(UUID(self.path.rsplit('/', 1)[1]))
                    row = db.execute('SELECT payload FROM payments WHERE ' + ('key' if by_info else 'id') + '=?', (key,)).fetchone()
                    if not row:
                        return self.reply(404, {'state': 1})
                    payment = json.loads(row[0])
                    if by_info:
                        if body != {'order_id': key}:
                            raise ValueError('info selector')
                        db.execute('UPDATE payments SET infos=infos+1 WHERE key=?', (key,))
                    else:
                        if body not in ({'status': 'paid'}, {'status': 'paid_over'}):
                            raise ValueError('settlement')
                        if payment['status'] != body['status']:
                            payment.update(status=body['status'], payment_status=body['status'], is_final=True,
                                           payment_amount='0.00002000' if body['status'] == 'paid' else '0.00003000',
                                           payer_amount='0.00002000', merchant_amount='0.00001960', payer_currency='BTC',
                                           updated_at=datetime.now(timezone.utc).isoformat())
                            db.execute('UPDATE payments SET payload=? WHERE id=?', (json.dumps(payment), key))
                    code, value = 200, {'state': 0, 'result': payment}
                else:
                    return self.reply(404, {'state': 1})
            return self.reply(code, value)
        except (ValueError, KeyError, TypeError):
            return self.reply(400, {'state': 1})

    do_GET = serve_request
    do_POST = serve_request


def self_check(provider='cryptomus'):
    with tempfile.TemporaryDirectory() as root:
        path = str(Path(root) / 'stub.db')
        initialize(path)
        server = ThreadingHTTPServer(('127.0.0.1', 0), Stub)
        server.db, server.key, server.merchant = path, 'synthetic-fixture-key', str(uuid4())
        server.provider = provider
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()

        def send(target, body=None, authorized=True):
            raw = b'' if body is None else body if isinstance(body, bytes) else json.dumps(body).encode()
            client = HTTPConnection(*server.server_address, timeout=3)
            client.request('GET' if body is None else 'POST', target, raw,
                           {'Content-Type': 'application/json', 'merchant': server.merchant,
                            'sign': sign(raw, server.key) if authorized else '0' * 32})
            response = client.getresponse()
            result = response.status, json.loads(response.read())
            client.close()
            return result

        try:
            key = str(uuid4())
            request = json.dumps({'order_id': key, 'amount': '2.00', 'currency': 'USD', 'lifetime': 1800,
                                  'is_payment_multiple': False, 'url_callback': 'https://localhost/webhooks/' + provider,
                                  'url_return': 'https://localhost/orders/' + key, 'url_success': 'https://localhost/orders/' + key}).encode()
            assert send('/v1/payment', request, authorized=False)[0] == 401
            assert send('/control', {'fail_next_creation': True})[0] == 200
            assert send('/v1/payment', request)[0] == 500
            code, value = send('/v1/payment', request)
            invoice = value['result']['uuid']
            assert code == 200 and value['result']['status'] == 'check'
            host = 'new-pay.heleket.com' if provider == 'heleket' else 'pay.cryptomus.com'
            assert value['result']['url'] == 'https://' + host + '/pay/' + invoice
            assert send('/v1/payment', request + b' ')[0] == 409
            assert send('/v1/payment/info', {'order_id': key})[1]['result']['uuid'] == invoice
            initialize(path)  # Reopening the persistent state does not recreate the invoice.
            rows = send('/control')[1]['payments']
            assert len(rows) == 1 and rows[0]['posts'] == 2 and rows[0]['request_sha256'] == sha256(request).hexdigest()
            for status in ['paid', 'paid_over']:
                assert send('/control/' + invoice, {'status': status})[0] == 200
                payment = send('/v1/payment/info', {'order_id': key})[1]['result']
                assert payment['status'] == payment['payment_status'] == status and payment['is_final']
                assert payment['currency'] == 'USD' and payment['payer_currency'] == 'BTC'
            assert send('/v1/payment/info', {'order_id': str(uuid4())})[0] == 404
        finally:
            server.shutdown()
            server.server_close()
            thread.join()
    print('PASS: ' + provider + ' signed API, own checkout host, frozen unique order, ambiguous500, persisted invoice, info and paid/paid_over')


def main():
    if sys.argv[1:] == ['--self-check']:
        for provider in ('cryptomus', 'heleket'):
            self_check(provider)
        return
    path = os.environ['STUB_DATABASE']
    initialize(path)
    server = ThreadingHTTPServer(('0.0.0.0', 443), Stub)
    server.provider = os.environ.get('STUB_PROVIDER', 'cryptomus')
    prefix = {'cryptomus': 'CRYPTOMUS', 'heleket': 'HELEKET'}[server.provider]
    server.db, server.merchant = path, os.environ[prefix + '_MERCHANT_ID']
    server.key = Path(os.environ[prefix + '_API_KEY_FILE']).read_text().strip()
    tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    tls.minimum_version = ssl.TLSVersion.TLSv1_2
    tls.load_cert_chain(os.environ['STUB_CERT_FILE'], os.environ['STUB_KEY_FILE'])
    server.socket = tls.wrap_socket(server.socket, server_side=True)
    server.serve_forever()


if __name__ == '__main__':
    main()
