"""Own cabinet-c14 acceptance: TLS stub, real River and native 3X-UI."""
import base64
from hashlib import md5, sha256
import importlib.util
import json
from pathlib import Path
import secrets
import ssl
import sys
import time
import traceback
import urllib.request
from urllib.error import URLError
from uuid import UUID, uuid4

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('c14_purchase_fixture', ROOT / 'deploy/purchase/local.py')
purchase = importlib.util.module_from_spec(spec)
spec.loader.exec_module(purchase)
local, bridge = purchase.local, purchase.bridge
assert local.PROFILE == 'native' and local.PROJECT == 'cabinet-c14', 'requires owned cabinet-c14 native project'
purchase.STATE = local.STATE / 'purchase'
bridge.STATE = local.STATE / 'roles'
MERCHANT = '00000000-0000-4000-8000-000000000014'


def compose(*args, stdin=None):
    return local.command(['docker', 'compose', '--project-name', local.PROJECT, '--profile', 'restore',
        '--env-file', str(local.ENV), '-f', 'deploy/acceptance/compose.acceptance.yml',
        '-f', 'deploy/acceptance/compose.local.yml', '-f', 'deploy/acceptance/compose.native.yml',
        '-f', 'deploy/purchase/compose.cryptomus.yml', '-f', 'deploy/acceptance/compose.cryptomus.yml',
        *args], stdin=stdin)


def prepare():
    local.prepare()
    config = dict(line.split('=', 1) for line in local.ENV.read_text().splitlines() if line and not line.startswith('#'))
    assert config['CABINET_ORIGIN'] == local.ORIGIN and config['CABINET_HOST'] == 'localhost'
    if not (local.STATE / 'cryptomus-key').exists():
        local.write('cryptomus-key', secrets.token_urlsafe(32) + '\n')
    cert, key = local.STATE / 'psp-cert.pem', local.STATE / 'psp-key.pem'
    if not cert.exists():
        local.command(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '30',
            '-subj', '/CN=api.cryptomus.com', '-addext', 'subjectAltName=DNS:api.cryptomus.com,DNS:localhost',
            '-keyout', str(key), '-out', str(cert)])
        key.chmod(0o600)
    local.write('psp-ca.pem', cert.read_text() + (local.STATE / 'cert.pem').read_text())
    (local.STATE / 'psp-data').mkdir(mode=0o700, exist_ok=True)
    config.update(APP_NETWORK_SUBNET='10.253.14.0/28', APP_GATEWAY_IP='10.253.14.14',
        SHOP_PAYMENT_CRYPTOMUS_ENABLED='true', CRYPTOMUS_MERCHANT_ID=MERCHANT,
        CRYPTOMUS_API_KEY_FILE=str(local.STATE / 'cryptomus-key'))
    local.write('public.env', '\n'.join(k + '=' + v for k, v in config.items()) + '\n')
    configured = json.loads(compose('config', '--format', 'json'))
    env = configured['services']['backend']['environment']
    assert env['SHOP_PAYMENT_CRYPTOMUS_ENABLED'] == 'true' and env['CRYPTOMUS_MERCHANT_ID'] == MERCHANT
    assert env['LEGACY_BOT_API_ENABLED'] == 'false' and env['TELEGRAM_ENABLED'] == 'false'
    assert configured['services']['cryptomus-stub']['networks']['default']['aliases'] == ['api.cryptomus.com']
    print('PASS: own file secrets, TLS/DNS API stub, no host trust or external provider', flush=True)


def control(path='/control', body=None):
    raw = b'' if body is None else json.dumps(body).encode()
    key = (local.STATE / 'cryptomus-key').read_text().strip()
    signature = md5(base64.b64encode(raw) + key.encode(), usedforsecurity=False).hexdigest()
    context = ssl.create_default_context(cafile=str(local.STATE / 'psp-cert.pem'))
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPSHandler(context=context))
    code, _, value = local.request(opener, 'https://localhost:58450' + path, body, {'merchant': MERCHANT, 'sign': signature})
    assert code == 200
    return json.loads(value)


def provider_row(order_id):
    order_id = str(UUID(order_id))
    try:
        rows = control()['payments']
    except URLError as error:
        if isinstance(error.reason, (ConnectionError, TimeoutError, ssl.SSLEOFError)):
            return None  # Only restart readiness; certificate/auth failures still fail.
        raise
    return next((row for row in rows if row['key'] == order_id), None)


def up():
    log = local.STATE / 'native-build.log'
    log.write_bytes(local.compose('build', 'backend', 'gateway'))
    log.chmod(0o600)
    local.up(reuse_images=True)
    compose('up', '--no-build', '--pull', 'never', '--no-deps', '-d', 'cryptomus-stub', 'backend', 'gateway')
    local.wait_until(local.ready)
    control()
    print('PASS: ordinary Go/web images and signed TLS API ready', flush=True)


def ready_order(opener, order_id):
    value = purchase.order(opener, order_id)
    return value if (value.get('cryptomus_checkout') or {}).get('state') == 'ready' else None


def create_order(opener, login, plan, manual=False):
    assert not manual
    body = {'action': 'purchase', 'plan_id': plan['plan_id'], 'revision': plan['revision'],
            'period_days': 30, 'payment_method': 'cryptomus', 'payment_type': 'CRYPTOMUS'}
    key = str(uuid4())
    status, _, value = local.api(opener, '/api/v1/orders', body, login['csrf_token'], key)
    assert status == 201 and value['quote']['amount_minor'] == '200' and value['quote']['currency'] == 'USD' and not value.get('checkout')
    assert local.api(opener, '/api/v1/orders', body, login['csrf_token'], key)[2]['order_id'] == value['order_id']
    return value


def settle_order(value, status='paid'):
    row = local.wait_until(lambda: provider_row(value['order_id']))
    control('/control/' + row['id'], {'status': status})
    # The real worker learns this through authenticated info, not a spoofed callback.
    local.wait_until(lambda: bridge.query("SELECT payment_status='paid' FROM purchase_orders WHERE id=:'order'::uuid;", order=value['order_id']) == 't', timeout=45)
    return 200, row['id']  # Successful local control request, not webhook delivery evidence.


def counts(order_id):
    return json.loads(bridge.query("""SELECT json_build_object(
      'receipts',(SELECT count(*) FROM purchase_receipts WHERE order_id=:'order'::uuid),
      'jobs',(SELECT count(*) FROM river_job WHERE kind='purchase_fulfillment' AND args->>'order_id'=:'order'),
      'provider_jobs',(SELECT count(*) FROM river_job WHERE kind='cryptomus_payment' AND args->>'order_id'=:'order'));""", order=str(UUID(order_id))))


def check():
    local.wait_until(local.ready)
    operator, actor = purchase.signup('operator')
    assert bridge.role('grant', actor['account']['account_id'])
    devices = int(bridge.query("""SELECT min(n) FROM generate_series(3,10000) n
        WHERE NOT EXISTS(SELECT 1 FROM catalogue_plans WHERE NOT archived AND current_devices=n);"""))
    terms = {'devices': devices, 'traffic_gb': 30, 'profile': 'regular', 'hidden': False, 'periods': [30],
             'prices': [{'period_days': 30, 'currency': currency, 'amount_minor': amount}
                        for currency, amount in [('RUB', '10000'), ('USD', '200'), ('XTR', '300')]]}
    code, _, plan = local.api(operator, '/api/v1/operator/catalogue/plans',
        {'terms': terms, 'reason': 'Own Cryptomus stub acceptance'}, actor['csrf_token'], str(uuid4()))
    assert code == 201
    purchase.private('plan.json', plan)
    results = []
    for name in ['new', 'trial']:
        opener, login = purchase.signup(name)
        account = login['account']['account_id']
        before = None
        if name == 'trial':
            _, _, trial = local.api(opener, '/api/v1/trial-requests', {'comment': 'Own Cryptomus conversion'}, login['csrf_token'], str(uuid4()))
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
            assert bridge.query("SELECT invoice_id IS NULL AND first_attempt_at IS NOT NULL FROM cryptomus_checkouts WHERE order_id=:'order'::uuid;", order=value['order_id']) == 't'
            compose('restart', 'backend')
            local.wait_until(local.ready)
        local.wait_until(lambda: ready_order(opener, value['order_id']), timeout=45)
        row = provider_row(value['order_id'])
        request_hex = bridge.query("SELECT encode(request,'hex') FROM cryptomus_checkouts WHERE order_id=:'order'::uuid;", order=value['order_id'])
        assert row['request_sha256'] == sha256(bytes.fromhex(request_hex)).hexdigest()
        if name == 'new':
            assert row['posts'] == 2
            compose('restart', 'cryptomus-stub')
            assert local.wait_until(lambda: provider_row(value['order_id']))['id'] == row['id']
        assert counts(value['order_id']) == {'receipts': 0, 'jobs': 0, 'provider_jobs': 1}
        settled_status = 'paid_over' if before else 'paid'
        settle_order(value, settled_status)
        final = local.wait_until(lambda: purchase.applied(opener, value['order_id']), timeout=60)
        after = purchase.panel(account)
        assert after['enabled'] and after['limit_ip'] == devices + 1 and after['traffic_bytes'] == 30 * 1024 ** 3
        if before:
            assert before['identity_digest'] == after['identity_digest'] and after['expiry_ms'] == before['expiry_ms'] + 30 * 86400000
            assert purchase.account_row(account)['grants'] == 1
        else:
            assert purchase.account_row(account)['grants'] == 0 and abs(after['expiry_ms'] - int(time.time() * 1000) - 30 * 86400000) < 60000
        # Retry the existing completed job in this owned fixture; do not add a second job or IP bypass.
        local.wait_until(lambda: bridge.query("SELECT state='completed' FROM river_job WHERE kind='cryptomus_payment' AND args->>'order_id'=:'order';", order=value['order_id']) == 't')
        info_before = provider_row(value['order_id'])['infos']
        bridge.query("UPDATE river_job SET state='available',scheduled_at=now(),finalized_at=NULL WHERE kind='cryptomus_payment' AND args->>'order_id'=:'order';", order=value['order_id'])
        local.wait_until(lambda: provider_row(value['order_id'])['infos'] > info_before)
        local.wait_until(lambda: bridge.query("SELECT state='completed' FROM river_job WHERE kind='cryptomus_payment' AND args->>'order_id'=:'order';", order=value['order_id']) == 't')
        assert purchase.panel(account) == after and counts(value['order_id']) == {'receipts': 1, 'jobs': 1, 'provider_jobs': 1}
        assert purchase.account_row(account)['access_operations'] == 1
        assert bridge.query("SELECT net_minor IS NULL AND currency='USD' AND gross_minor=200 FROM purchase_receipts WHERE order_id=:'order'::uuid;", order=value['order_id']) == 't'
        purchase.private(name + '-order.json', final)
        results.append({'fixture': name, 'paid_status': settled_status, 'applied': True, 'one_receipt_job_access': True,
                        'identity_preserved': before is not None, 'same_bytes_after_restart': name == 'new',
                        'same_job_info_replay_unchanged': True})
    purchase.private('native-report.json', {'panel_version': '3.7.0', 'results': results, 'api_stub': True,
        'native_vendor_webhook': False, 'real_payment': False, 'live_vpn_changed': False})
    print('PASS: API/River/info/native3X-UI new/paid and trial/paid_over, restart, same bytes/invoice, one receipt/job/access', flush=True)


original_snapshot = purchase.financial_snapshot


def financial_snapshot(db=None):
    result = original_snapshot(db)
    result['provider_digest'] = bridge.query("SELECT md5(coalesce(jsonb_agg(to_jsonb(c) ORDER BY order_id)::text,'[]')) FROM cryptomus_checkouts c;", db=db)
    return result


def restore():
    purchase.compose, purchase.create_order = compose, create_order
    purchase.notification, purchase.financial_snapshot = settle_order, financial_snapshot
    purchase.restore()


def main():
    actions = {'prepare': prepare, 'up': up, 'check': check, 'restore': restore, 'stop': lambda: compose('stop')}
    if len(sys.argv) != 2 or sys.argv[1] not in actions:
        raise ValueError('use prepare/up/check/restore/stop with LOCAL_STATE_DIR of cabinet-c14')
    actions[sys.argv[1]]()


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        frames = ', '.join(Path(f.filename).name + ':' + str(f.lineno) + ':' + f.name for f in traceback.extract_tb(error.__traceback__))
        purchase.private('failure.json', {'traceback': traceback.format_exc()})
        print('FAIL: own Cryptomus acceptance ' + type(error).__name__ + ' at ' + frames, file=sys.stderr)
        raise SystemExit(1)
