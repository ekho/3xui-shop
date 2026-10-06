"""Own disposable YooKassa API stub. Never calls an external provider."""
import base64
from datetime import datetime, timezone
from hashlib import sha256
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
from uuid import UUID, uuid4


def initialize(path):
    with sqlite3.connect(path) as db:
        db.execute('CREATE TABLE IF NOT EXISTS payments (key TEXT PRIMARY KEY,id TEXT UNIQUE,request BLOB,payload TEXT,posts INTEGER DEFAULT 1,gets INTEGER DEFAULT 0)')
        db.execute('CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY,value INTEGER)')


class Stub(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass  # Payment IDs, bodies and auth never enter the container log.

    def reply(self, code, value):
        body = json.dumps(value).encode()
        self.send_response(code)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def serve_request(self):
        auth = 'Basic ' + base64.b64encode((self.server.shop + ':' + self.server.token).encode()).decode()
        if self.headers.get('Authorization') != auth:
            return self.reply(401, {'error': 'unauthorized'})
        try:
            raw = b''
            body = None
            if self.command == 'POST':
                length = int(self.headers.get('Content-Length', '0'))
                if not 0 < length <= 16384:
                    raise ValueError('body size')
                raw = self.rfile.read(length)
                body = json.loads(raw.decode('utf-8'))
                if not isinstance(body, dict):
                    raise ValueError('body type')
            with sqlite3.connect(self.server.db, timeout=10) as db:
                db.execute('BEGIN IMMEDIATE')
                if self.path == '/control':
                    if self.command == 'POST':
                        if body != {'fail_next_creation': True}:
                            raise ValueError('control action')
                        db.execute("INSERT INTO settings VALUES('fail_next',1) ON CONFLICT(key) DO UPDATE SET value=1")
                        code, value = 200, {'armed': True}
                    else:
                        rows = db.execute('SELECT key,id,request,posts,gets,payload FROM payments ORDER BY key').fetchall()
                        code, value = 200, {'payments': [{'key': key, 'id': pid, 'request_sha256': sha256(request).hexdigest(), 'posts': posts, 'gets': gets, 'status': json.loads(payload)['status']} for key, pid, request, posts, gets, payload in rows]}
                elif self.path == '/v3/payments' and self.command == 'POST':
                    key = str(UUID(self.headers['Idempotence-Key']))
                    if body['metadata']['order_id'] != key or body['amount']['currency'] != 'RUB' or body['capture'] is not True or body['save_payment_method'] is not False:
                        raise ValueError('payment request')
                    row = db.execute('SELECT id,request,payload FROM payments WHERE key=?', (key,)).fetchone()
                    if row:
                        if row[1] != raw:
                            return self.reply(409, {'error': 'key_body_conflict'})
                        db.execute('UPDATE payments SET posts=posts+1 WHERE key=?', (key,))
                        code, value = 200, json.loads(row[2])
                    else:
                        pid = str(uuid4())
                        value = {'id': pid, 'status': 'pending', 'paid': False, 'test': True, 'refundable': False,
                                 'amount': body['amount'], 'created_at': datetime.now(timezone.utc).isoformat(),
                                 'recipient': {'account_id': self.server.shop}, 'metadata': {'order_id': key},
                                 'confirmation': {'type': 'redirect', 'confirmation_url': 'https://yoomoney.ru/checkout/payments/' + pid}}
                        db.execute('INSERT INTO payments(key,id,request,payload) VALUES(?,?,?,?)', (key, pid, raw, json.dumps(value)))
                        fault = db.execute("SELECT value FROM settings WHERE key='fail_next'").fetchone()
                        code = 500 if fault and fault[0] else 200
                        db.execute("DELETE FROM settings WHERE key='fail_next'")
                elif self.path.startswith('/v3/payments/') and self.command == 'GET' or self.path.startswith('/control/') and self.command == 'POST':
                    pid = str(UUID(self.path.rsplit('/', 1)[1]))
                    row = db.execute('SELECT payload FROM payments WHERE id=?', (pid,)).fetchone()
                    if not row:
                        return self.reply(404, {'error': 'unknown_payment'})
                    value = json.loads(row[0])
                    if self.command == 'POST':
                        if body not in ({'status': 'succeeded'}, {'status': 'pending'}, {'status': 'canceled'}):
                            raise ValueError('control status')
                        value['status'], value['paid'] = body['status'], body['status'] == 'succeeded'
                        if value['paid']:
                            value['captured_at'] = datetime.now(timezone.utc).isoformat()
                        else:
                            value.pop('captured_at', None)
                        db.execute('UPDATE payments SET payload=? WHERE id=?', (json.dumps(value), pid))
                    else:
                        db.execute('UPDATE payments SET gets=gets+1 WHERE id=?', (pid,))
                    code = 200
                else:
                    return self.reply(404, {'error': 'unknown_route'})
            return self.reply(code, value)
        except (ValueError, KeyError, TypeError):
            return self.reply(400, {'error': 'invalid_input'})

    do_GET = serve_request
    do_POST = serve_request


def self_check():
    with tempfile.TemporaryDirectory() as root:
        path = str(Path(root) / 'stub.db')
        initialize(path)
        server = ThreadingHTTPServer(('127.0.0.1', 0), Stub)
        server.db, server.token, server.shop = path, 'synthetic-fixture-token', '100001'
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        def send(method, target, body=None, key=None, authorized=True):
            client = HTTPConnection(*server.server_address, timeout=3)
            headers = {'Content-Type': 'application/json'}
            if authorized:
                headers['Authorization'] = 'Basic ' + base64.b64encode((server.shop + ':' + server.token).encode()).decode()
            if key:
                headers['Idempotence-Key'] = key
            client.request(method, target, body, headers)
            response = client.getresponse()
            raw = response.read()
            value = json.loads(raw) if response.getheader('Content-Type', '').startswith('application/json') else None
            client.close()
            return response.status, value
        try:
            key = str(uuid4())
            request = json.dumps({'amount': {'value': '100.00', 'currency': 'RUB'}, 'capture': True,
                                  'save_payment_method': False, 'metadata': {'order_id': key},
                                  'confirmation': {'type': 'redirect', 'return_url': 'https://localhost/orders/' + key},
                                  'receipt': {'customer': {'email': 'receipt@example.test'}, 'items': []}}).encode()
            code, first = send('POST', '/v3/payments', request, key)
            assert code == 200 and first['status'] == 'pending' and first['test'], 'stub creation unavailable'
            assert send('POST', '/v3/payments', request, key)[1]['id'] == first['id'], 'same key recreated payment'
            assert send('POST', '/v3/payments', request + b' ', key)[0] == 409, 'changed frozen bytes accepted'
            assert send('GET', '/v3/payments/' + first['id'], authorized=False)[0] == 401, 'API auth missing'
            assert send('POST', '/control', b'{"fail_next_creation":true}')[0] == 200
            second_key = str(uuid4())
            second_request = request.replace(key.encode(), second_key.encode())
            assert send('POST', '/v3/payments', second_request, second_key)[0] == 500, 'ambiguous creation fault missing'
            code, recovered = send('POST', '/v3/payments', second_request, second_key)
            assert code == 200, 'ambiguous creation recovery failed'
            rows = send('GET', '/control')[1]['payments']
            recovered_row = next(row for row in rows if row['id'] == recovered['id'])
            assert len(rows) == 2 and recovered_row['posts'] == 2 and recovered_row['request_sha256'] == sha256(second_request).hexdigest()
            assert send('POST', '/control/' + recovered['id'], b'{"status":"succeeded"}')[0] == 200
            settled = send('GET', '/v3/payments/' + recovered['id'])[1]
            assert settled['status'] == 'succeeded' and settled['paid'] and settled['captured_at']
            assert send('GET', '/v3/payments/' + str(uuid4()))[0] == 404
        finally:
            server.shutdown()
            server.server_close()
            thread.join()
    print('PASS: stub auth, frozen bytes/key, ambiguous500 recovery, durable facts and authorized status')


def main():
    if sys.argv[1:] == ['--self-check']:
        self_check()
        return
    path = os.environ['STUB_DATABASE']
    initialize(path)
    server = ThreadingHTTPServer(('0.0.0.0', 443), Stub)
    server.db = path
    server.token = Path(os.environ['YOOKASSA_TOKEN_FILE']).read_text().strip()
    server.shop = os.environ['YOOKASSA_SHOP_ID']
    tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    tls.minimum_version = ssl.TLSVersion.TLSv1_2
    tls.load_cert_chain(os.environ['STUB_CERT_FILE'], os.environ['STUB_KEY_FILE'])
    server.socket = tls.wrap_socket(server.socket, server_side=True)
    server.serve_forever()


if __name__ == '__main__':
    main()
