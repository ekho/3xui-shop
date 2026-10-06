"""Own cabinet-c12 native acceptance, using a local TLS API stub only."""
import base64
from hashlib import sha256
import importlib.util
import json
import os
from pathlib import Path
import secrets
import ssl
import sys
import time
import traceback
import urllib.request
from urllib.error import HTTPError, URLError
from uuid import UUID, uuid4

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('c12_purchase_fixture', ROOT / 'deploy/purchase/local.py')
purchase = importlib.util.module_from_spec(spec)
spec.loader.exec_module(purchase)
local, bridge = purchase.local, purchase.bridge
assert local.PROFILE == 'native' and local.PROJECT == 'cabinet-c12', 'requires the owned cabinet-c12 native project'
purchase.STATE = local.STATE / 'purchase'
bridge.STATE = local.STATE / 'roles'
STATE = purchase.STATE


def compose(*args, stdin=None):
    return local.command(['docker', 'compose', '--project-name', local.PROJECT, '--profile', 'restore',
        '--env-file', str(local.ENV), '-f', 'deploy/acceptance/compose.acceptance.yml',
        '-f', 'deploy/acceptance/compose.local.yml', '-f', 'deploy/acceptance/compose.native.yml',
        '-f', 'deploy/purchase/compose.yookassa.yml', '-f', 'deploy/acceptance/compose.yookassa.yml',
        *args], stdin=stdin)


def prepare():
    local.prepare()
    config = dict(line.split('=', 1) for line in local.ENV.read_text().splitlines() if line and not line.startswith('#'))
    assert config['CABINET_ORIGIN'] == local.ORIGIN and config['CABINET_HOST'] == 'localhost'
    if not (local.STATE / 'yookassa-token').exists():
        local.write('yookassa-token', secrets.token_urlsafe(32) + '\n')
    cert, key = local.STATE / 'psp-cert.pem', local.STATE / 'psp-key.pem'
    if not cert.exists():
        local.command(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '30',
            '-subj', '/CN=api.yookassa.ru', '-addext', 'subjectAltName=DNS:api.yookassa.ru,DNS:localhost',
            '-keyout', str(key), '-out', str(cert)])
        key.chmod(0o600)
    local.write('psp-ca.pem', cert.read_text() + (local.STATE / 'cert.pem').read_text())
    (local.STATE / 'psp-data').mkdir(mode=0o700, exist_ok=True)
    config.update(APP_NETWORK_SUBNET='10.253.12.0/28', APP_GATEWAY_IP='10.253.12.14',
        SHOP_PAYMENT_YOOKASSA_ENABLED='true', YOOKASSA_TEST_MODE='true', YOOKASSA_SHOP_ID='100001',
        SHOP_EMAIL='receipts@example.test', YOOKASSA_TOKEN_FILE=str(local.STATE / 'yookassa-token'))
    local.write('public.env', '\n'.join(k + '=' + v for k, v in config.items()) + '\n')
    configured = json.loads(compose('config', '--format', 'json'))
    env = configured['services']['backend']['environment']
    assert env['SHOP_PAYMENT_YOOKASSA_ENABLED'] == 'true' and env['YOOKASSA_TEST_MODE'] == 'true'
    assert env['LEGACY_BOT_API_ENABLED'] == 'false' and env['TELEGRAM_ENABLED'] == 'false'
    assert configured['services']['yookassa-stub']['networks']['default']['aliases'] == ['api.yookassa.ru']
    print('PASS: own TLS/DNS stub overlay; file secrets, no host trust or external provider')


def control(path='/control', body=None):
    token = (local.STATE / 'yookassa-token').read_text().strip()
    auth = 'Basic ' + base64.b64encode(('100001:' + token).encode()).decode()
    context = ssl.create_default_context(cafile=str(local.STATE / 'psp-cert.pem'))
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPSHandler(context=context))
    return json.loads(local.request(opener, 'https://localhost:58449' + path, body, {'Authorization': auth})[2])


def provider_row(order_id):
    order_id = str(UUID(order_id))
    try:
        rows = control()['payments']
    except URLError as error:
        if isinstance(error.reason, (ConnectionError, TimeoutError, ssl.SSLEOFError)):
            return None  # The existing bounded wait handles a restarting stub.
        raise  # Auth and certificate errors are not readiness delays.
    return next((row for row in rows if row['key'] == order_id), None)


def up():
    log = local.STATE / 'native-build.log'
    log.write_bytes(local.compose('build', 'backend', 'gateway'))
    log.chmod(0o600)
    local.up(reuse_images=True)
    compose('up', '--no-build', '--pull', 'never', '--no-deps', '-d', 'yookassa-stub', 'backend', 'gateway')
    local.wait_until(local.ready)
    control()
    print('PASS: one Go process and authenticated TLS API stub ready')


def ready_order(opener, order_id):
    value = purchase.order(opener, order_id)
    return value if value.get('yookassa_checkout', {}).get('state') == 'ready' else None


def create_order(opener, login, plan, manual=False):
    assert not manual
    body = {'action': 'purchase', 'plan_id': plan['plan_id'], 'revision': plan['revision'],
            'period_days': 30, 'payment_method': 'yookassa', 'payment_type': 'YOOKASSA'}
    key = str(uuid4())
    status, _, value = local.api(opener, '/api/v1/orders', body, login['csrf_token'], key)
    assert status == 201 and value['quote']['amount_minor'] == '10000' and not value.get('checkout')
    assert local.api(opener, '/api/v1/orders', body, login['csrf_token'], key)[2]['order_id'] == value['order_id']
    return value


def notification(value, *, settle=True):
    row = local.wait_until(lambda: provider_row(value['order_id']))
    if settle:
        control('/control/' + row['id'], {'status': 'succeeded'})
    notice = {'type': 'notification', 'event': 'payment.succeeded',
              'object': {'id': row['id'], 'status': 'succeeded', 'paid': True}}
    def deliver():
        try:
            return local.request(local.session(), local.ORIGIN + '/webhooks/yookassa', notice)[0]
        except HTTPError as error:
            if error.code != 503:
                raise
            return None  # YooKassa retries an unacknowledged notification.
    return local.wait_until(deliver, timeout=30), row['id']


def counts(order_id):
    return json.loads(bridge.query("""SELECT json_build_object(
      'receipts',(SELECT count(*) FROM purchase_receipts WHERE order_id=:'order'::uuid),
      'jobs',(SELECT count(*) FROM river_job WHERE kind='purchase_fulfillment' AND args->>'order_id'=:'order'),
      'provider_jobs',(SELECT count(*) FROM river_job WHERE kind='yookassa_payment' AND args->>'order_id'=:'order'));""", order=order_id))


def check():
    local.wait_until(local.ready)
    operator, actor = purchase.signup('operator')
    assert bridge.role('grant', actor['account']['account_id'])
    devices = int(bridge.query("""SELECT min(n) FROM generate_series(3,10000) n
        WHERE NOT EXISTS(SELECT 1 FROM catalogue_plans WHERE NOT archived AND current_devices=n);"""))
    terms = {'devices': devices, 'traffic_gb': 30, 'profile': 'regular', 'hidden': False, 'periods': [30],
             'prices': [{'period_days': 30, 'currency': currency, 'amount_minor': amount}
                        for currency, amount in [('RUB', '10000'), ('USD', '200'), ('XTR', '300')]]}
    status, _, plan = local.api(operator, '/api/v1/operator/catalogue/plans',
        {'terms': terms, 'reason': 'Own YooKassa stub acceptance'}, actor['csrf_token'], str(uuid4()))
    assert status == 201
    purchase.private('plan.json', plan)
    results = []
    for name in ['new', 'trial']:
        opener, login = purchase.signup(name)
        account = login['account']['account_id']
        before = None
        if name == 'trial':
            _, _, trial = local.api(opener, '/api/v1/trial-requests', {'comment': 'Own YooKassa conversion'}, login['csrf_token'], str(uuid4()))
            local.api(operator, '/api/v1/operator/trial-requests/' + trial['request_id'] + '/decision',
                      {'decision': 'approve', 'reason': ''}, actor['csrf_token'], str(uuid4()))
            local.wait_until(lambda: local.active(opener))
            before = purchase.panel(account)
        else:
            control(body={'fail_next_creation': True})
        value = create_order(opener, login, plan)
        if name == 'new':
            row = local.wait_until(lambda: provider_row(value['order_id']))
            assert row['posts'] == 1
            snapshot = bridge.query("SELECT payment_id IS NULL AND first_attempt_at IS NOT NULL FROM yookassa_checkouts WHERE order_id=:'order'::uuid;", order=value['order_id'])
            assert snapshot == 't', 'ambiguous API creation was not retained'
            compose('restart', 'backend')
            local.wait_until(local.ready)
        local.wait_until(lambda: ready_order(opener, value['order_id']), timeout=45)
        row = provider_row(value['order_id'])
        request_hex = bridge.query("SELECT encode(request,'hex') FROM yookassa_checkouts WHERE order_id=:'order'::uuid;", order=value['order_id'])
        assert row['request_sha256'] == sha256(bytes.fromhex(request_hex)).hexdigest()
        if name == 'new':
            assert row['posts'] == 2
            compose('restart', 'yookassa-stub')
            assert local.wait_until(lambda: provider_row(value['order_id']))['id'] == row['id']
        assert notification(value, settle=False)[0] == 200
        assert counts(value['order_id']) == {'receipts': 0, 'jobs': 0, 'provider_jobs': 1}, 'forged notice funded pending API state'
        assert notification(value)[0] == 200
        final = local.wait_until(lambda: purchase.applied(opener, value['order_id']), timeout=60)
        after = purchase.panel(account)
        assert after['enabled'] and after['limit_ip'] == devices + 1 and after['traffic_bytes'] == 30 * 1024 ** 3
        if before:
            assert before['identity_digest'] == after['identity_digest'] and after['expiry_ms'] == before['expiry_ms'] + 30 * 86400000
            assert purchase.account_row(account)['grants'] == 1
        else:
            assert purchase.account_row(account)['grants'] == 0 and abs(after['expiry_ms'] - int(time.time() * 1000) - 30 * 86400000) < 60000
        assert notification(value, settle=False)[0] == 200 and purchase.panel(account) == after
        assert counts(value['order_id']) == {'receipts': 1, 'jobs': 1, 'provider_jobs': 1}
        assert purchase.account_row(account)['access_operations'] == 1
        assert bridge.query("SELECT net_minor IS NULL FROM purchase_receipts WHERE order_id=:'order'::uuid;", order=value['order_id']) == 't'
        purchase.private(name + '-order.json', final)
        results.append({'fixture': name, 'paid': True, 'applied': True, 'one_receipt_job_access': True,
                        'identity_preserved': before is not None, 'replay_unchanged': True,
                        'same_bytes_after_restart': name == 'new'})
    purchase.private('native-report.json', {'panel_version': '3.7.0', 'results': results,
        'api_stub': True, 'real_payment': False, 'live_vpn_changed': False})
    print('PASS: own API/River/native3X-UI new/trial, ambiguous500/backend+stub restart, exact bytes, replay and unknown net')


original_snapshot = purchase.financial_snapshot


def financial_snapshot(db=None):
    result = original_snapshot(db)
    result['provider_digest'] = bridge.query("SELECT md5(coalesce(jsonb_agg(to_jsonb(c) ORDER BY order_id)::text,'[]')) FROM yookassa_checkouts c;", db=db)
    return result


def restore():
    # Reuse the existing quiesce/dump/read-only restore/auth-maintenance sequence
    # on this independently imported fixture instance, with its YooKassa transport.
    purchase.compose, purchase.create_order = compose, create_order
    purchase.notification, purchase.financial_snapshot = notification, financial_snapshot
    purchase.restore()


def main():
    actions = {'prepare': prepare, 'up': up, 'check': check, 'restore': restore,
               'down': lambda: compose('down', '--volumes', '--remove-orphans')}
    if len(sys.argv) != 2 or sys.argv[1] not in actions:
        raise ValueError('use prepare/up/check/restore/down with LOCAL_STATE_DIR of cabinet-c12')
    actions[sys.argv[1]]()


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        frames = ', '.join(Path(f.filename).name + ':' + str(f.lineno) + ':' + f.name
                           for f in traceback.extract_tb(error.__traceback__))
        print('FAIL: own YooKassa acceptance ' + type(error).__name__ + ' at ' + frames, file=sys.stderr)
        raise SystemExit(1)
