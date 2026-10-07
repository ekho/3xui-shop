"""Owned cabinet-c17 plan-change acceptance; localhost fixtures and native 3X-UI only."""
from concurrent.futures import ThreadPoolExecutor
from hashlib import sha256
import importlib.util
import json
import os
from pathlib import Path
import socket
import subprocess
import sys
import time
import traceback
from uuid import uuid4

ROOT = Path(__file__).resolve().parents[2]
STATE = Path(os.environ.get('LOCAL_STATE_DIR', ROOT / '.superpowers/acceptance/c17-plan-change/native')).resolve()
assert STATE.is_relative_to(ROOT / '.superpowers/acceptance/c17-plan-change'), 'requires own c17 state'
STATE.mkdir(mode=0o700, parents=True, exist_ok=True)
STATE.chmod(0o700)
runtime = STATE / 'runtime.json'
if not runtime.exists():
    runtime.write_text(json.dumps({'project': 'cabinet-c17', 'postgres_user': 'cabinet_c17',
        'base_database': 'cabinet_c17', 'fixture_prefixes': {'purchase': 'c17-purchase-'}}))
    runtime.chmod(0o600)
os.environ['LOCAL_STATE_DIR'] = str(STATE)
spec = importlib.util.spec_from_file_location('c17_purchase_fixture', ROOT / 'deploy/purchase/local.py')
purchase = importlib.util.module_from_spec(spec)
spec.loader.exec_module(purchase)
local, bridge = purchase.local, purchase.bridge
assert local.PROFILE == 'native' and local.PROJECT == 'cabinet-c17'
assert local.PG_USER == local.BASE_DATABASE == 'cabinet_c17'
purchase.STATE, bridge.STATE = STATE / 'purchase', STATE / 'roles'
spec = importlib.util.spec_from_file_location('c17_native_payload', ROOT / 'deploy/subscription-operations/local.py')
operations = importlib.util.module_from_spec(spec)
spec.loader.exec_module(operations)


def compose(*args, stdin=None):
    return local.command(['docker', 'compose', '--project-name', local.PROJECT, '--profile', 'restore',
        '--env-file', str(local.ENV), '-f', 'deploy/acceptance/compose.acceptance.yml',
        '-f', 'deploy/acceptance/compose.local.yml', '-f', 'deploy/acceptance/compose.native.yml',
        '-f', 'deploy/purchase/compose.yoomoney.yml', '-f', str(STATE / 'images.json'),
        *args], stdin=stdin)


local.compose = purchase.compose = compose


def prepare():
    endpoint = subprocess.check_output(['docker', 'context', 'inspect', '--format',
        '{{.Endpoints.docker.Host}}'], text=True).strip()
    assert os.environ.get('DOCKER_HOST', endpoint).startswith('unix://'), 'requires local Docker'
    local.prepare()
    config = dict(line.split('=', 1) for line in local.ENV.read_text().splitlines()
                  if line and not line.startswith('#'))
    config.update(APP_NETWORK_SUBNET='10.253.17.0/28', APP_GATEWAY_IP='10.253.17.14',
        BOT_OPERATOR_IDS='', TELEGRAM_ENABLED='false')
    local.write('public.env', '\n'.join(k + '=' + v for k, v in config.items()) + '\n')
    revision = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    label = {'org.opencontainers.image.revision': revision}
    backend = {'image': 'cabinet-c17-backend:local', 'build': {'labels': label}}
    gateway = {'image': 'cabinet-c17-web:local', 'build': {'labels': label}}
    local.write('images.json', json.dumps({'services': {'backend': backend, 'migrate': backend,
        'reconcile': backend, 'gateway': gateway, 'origin': {'image': gateway['image']}}}))
    purchase.prepare()
    configured = json.loads(compose('config', '--format', 'json'))
    env = configured['services']['backend']['environment']
    assert env['LEGACY_BOT_API_ENABLED'] == 'false' and env['TELEGRAM_ENABLED'] == 'false'
    assert configured['services']['panel']['image'] == 'ghcr.io/mhsanaei/3x-ui:3.7.0@sha256:3b3131f1876e6bf35063a9ec4dd1c594e4525180bfc2e1c477dcc8a3c9550ca1'
    paths = subprocess.check_output(['git', 'ls-files', 'backend', 'web'], cwd=ROOT, text=True).splitlines()
    inputs = {p: sha256((ROOT / p).read_bytes()).hexdigest() for p in paths}
    purchase.private('image-source.json', {'revision': revision, 'inputs': inputs})
    print('PASS: own c17 identity, pinned3.7.0, file credentials and synthetic YooMoney')


def up():
    for port in (58443, 59444, 59445, 59446, 59447):
        with socket.socket() as probe:
            probe.bind(('127.0.0.1', port))
    log = STATE / 'native-build.log'
    log.write_bytes(compose('build', 'backend', 'gateway'))
    log.chmod(0o600)
    local.up(reuse_images=True)
    ids = [compose('ps', '-q', name).decode().strip() for name in ('backend', 'gateway', 'panel')]
    containers = json.loads(local.command(['docker', 'inspect', *ids]))
    source = json.loads((purchase.STATE / 'image-source.json').read_text())
    images = json.loads(local.command(['docker', 'image', 'inspect', *[c['Image'] for c in containers]]))
    assert all(c['Config']['Labels']['com.docker.compose.project'] == 'cabinet-c17' for c in containers)
    assert all(i['Config']['Labels']['org.opencontainers.image.revision'] == source['revision'] for i in images[:2])
    assert all(sha256((ROOT / p).read_bytes()).hexdigest() == digest for p, digest in source['inputs'].items())
    assert containers[0]['Config']['Cmd'] == ['serve']
    panel, csrf = local.login_panel()
    inbounds = local.panel_call(panel, 'panel/api/inbounds/list')
    if not any(row['tag'] == 'local-euru-vless' for row in inbounds):
        regular = next(row for row in inbounds if row['tag'] == 'local-regular-vless')
        body = {k: regular[k] for k in ('enable', 'listen', 'protocol', 'settings', 'streamSettings', 'sniffing')}
        body.update(port=24445, tag='local-euru-vless', remark='Owned plan-change EURU')
        body['settings'] = {**body['settings'], 'clients': []}
        local.panel_call(panel, 'panel/api/inbounds/add', body, csrf)
    purchase.private('images.json', {'revision': source['revision'], 'ids': [c['Image'] for c in containers],
        'one_backend': True, 'native_version': '3.7.0'})
    print('PASS: own Go/web source labels, native3.7.0 and regular/EURU; one backend')


def native(account):
    row = purchase.account_row(account)
    panel, _ = local.login_panel()
    value = local.panel_call(panel, 'panel/api/clients/get/' + row['panel_key'])
    assert value['client']['uuid'] == row['vpn_id'] and value['client']['subId'] == row['sub_id']
    assert isinstance(value['usedTraffic'], int)
    return value


def expire_or_exhaust(account, exhausted):
    row = purchase.account_row(account)
    before = native(account)
    panel, csrf = local.login_panel()
    if not exhausted:
        payload = operations.native_update_payload(row, before['client'])
        local.panel_call(panel, 'panel/api/clients/update/' + row['panel_key'],
            {**payload, 'expiryTime': int(time.time() * 1000) - 86400000}, csrf)
    local.panel_call(panel, 'panel/api/clients/bulkDisable', {'emails': [row['panel_key']]}, csrf)
    if exhausted:
        purchase.exhaust_counters(account, before['client']['totalGB'], len(before['inboundIds']))
    after = native(account)
    assert after['client']['enable'] is False and after['inboundIds'] == before['inboundIds']
    if exhausted:
        assert after['usedTraffic'] >= after['client']['totalGB']
    else:
        assert after['client']['expiryTime'] < int(time.time() * 1000)


def paid(value):
    status, receipt = purchase.notification(value)
    assert status == 200
    with ThreadPoolExecutor(max_workers=2) as executor:
        assert all(result[0] == 200 for result in executor.map(lambda _: purchase.notification(value, receipt=receipt), range(2)))
    purchase.private('notice-' + value['order_id'] + '.json', {'receipt': receipt, 'fields': purchase.NOTICES[receipt]})
    return receipt


def change(opener, login, plan):
    context = local.api(opener, '/api/v1/subscription/plan-change')[2]
    body = {'action': 'change_plan', 'plan_id': plan['plan_id'], 'revision': plan['revision'],
        'period_days': 30, 'payment_method': 'yoomoney', 'payment_type': 'PC',
        'source_access_operation_id': context['source_access_operation_id']}
    key = str(uuid4())
    status, _, value = local.api(opener, '/api/v1/orders', body, login['csrf_token'], key)
    assert status == 201 and value['action'] == 'change_plan' and value['quote']['amount_minor'] == '10000'
    assert value['quote']['source_access_operation_id'] == context['source_access_operation_id']
    assert local.api(opener, '/api/v1/orders', body, login['csrf_token'], key)[2]['order_id'] == value['order_id']
    assert purchase.manual_api(opener, '/api/v1/orders', {**body, 'payment_type': 'AC'}, login['csrf_token'], key)[0] == 409
    purchase.private('change-request-' + value['order_id'] + '.json', {'body': body, 'key': key})
    return value


def proof(value, db=None):
    return json.loads(bridge.query("""BEGIN READ ONLY;
      SELECT json_build_object('action',p.action,'source',p.quote->>'source_access_operation_id',
        'quote_digest',md5(p.quote::text),'paid',p.payment_status='paid',
        'funded',p.funding_operation_id IS NOT NULL,'fulfillment',p.fulfillment_status,
        'operation',p.access_operation_id,'target',a.target,'steps',a.completed_steps,
        'reset_started',a.reset_started,'access_status',a.status,
        'receipts',(SELECT count(*) FROM purchase_receipts r WHERE r.order_id=p.id),
        'jobs',(SELECT count(*) FROM river_job j WHERE j.kind='access_operation'
          AND j.args->>'operation_id'=a.id::text),'target_digest',md5(a.target::text))
      FROM purchase_orders p LEFT JOIN access_operations a ON a.id=p.access_operation_id
      WHERE p.id=:'order'::uuid; ROLLBACK;""", db=db, order=value['order_id']))


def assert_applied(opener, login, value, before, earliest):
    result = local.wait_until(lambda: purchase.applied(opener, value['order_id']), timeout=60)
    account = login['account']['account_id']
    after = purchase.panel(account)
    assert result['payment_status'] == 'paid' and result['action'] == 'change_plan'
    assert after['identity_digest'] == before['identity_digest'] and after['enabled']
    assert after['limit_ip'] == value['quote']['devices'] + 1
    assert after['traffic_bytes'] == value['quote']['traffic_gb'] * 1024**3 and native(account)['usedTraffic'] == 0
    saved = proof(value)
    assert saved['source'] == value['quote']['source_access_operation_id'] and saved['paid'] and saved['funded']
    assert saved['receipts'] == saved['jobs'] == 1 and saved['access_status'] == 'applied'
    assert saved['target']['expiry_time_ms'] == after['expiry_ms'] and saved['target']['reset'] and saved['reset_started']
    duration = value['quote']['period_days'] * 86400000
    assert earliest + duration <= after['expiry_ms'] <= int(time.time() * 1000) + duration
    if before['expiry_ms'] > earliest:
        assert after['expiry_ms'] < before['expiry_ms'] + duration
    assert after['memberships'] == sorted(saved['target']['inbound_ids'])
    context = local.api(opener, '/api/v1/subscription/plan-change')[2]
    assert context == {'current_plan_id': value['quote']['plan_id'], 'source_access_operation_id': saved['operation']}
    receipt = json.loads((purchase.STATE / ('notice-' + value['order_id'] + '.json')).read_text())['receipt']
    assert purchase.notification(value, receipt=receipt)[0] == 200
    assert proof(value) == saved and purchase.panel(account) == after
    return {'applied': True, 'single_target_receipt_job': True, 'replay_unchanged': True,
        'identity_preserved': True, 'reset_zero': True, 'source_preserved': True,
        'now_based_expiry': True, 'target_digest': saved['target_digest']}


def operation(operator, actor, account, body):
    path = '/api/v1/operator/clients/' + purchase.owned(account) + '/access-operations'
    status, _, value = local.api(operator, path, {**body, 'reason': 'Owned synthetic change guard'},
        actor['csrf_token'], str(uuid4()))
    assert status == 202
    def applied():
        current = local.api(operator, path + '/' + value['operation_id'])[2]
        assert current['status'] != 'needs_review'
        return current if current['status'] == 'applied' else None
    return local.wait_until(applied, timeout=60)


def check():
    local.wait_until(local.ready)
    operator, actor = purchase.signup('operator')
    assert bridge.role('grant', actor['account']['account_id'])
    available = bridge.query("SELECT n FROM generate_series(3,10000) n WHERE NOT EXISTS(SELECT 1 FROM catalogue_plans WHERE NOT archived AND current_devices=n) ORDER BY n LIMIT 2;").splitlines()
    plans = {}
    for profile, devices in zip(('regular', 'euru'), map(int, available), strict=True):
        terms = {'devices': devices, 'traffic_gb': 30 if profile == 'regular' else 120, 'profile': profile,
            'hidden': False, 'periods': [30], 'prices': [{'period_days': 30, 'currency': currency, 'amount_minor': minor}
                for currency, minor in [('RUB', '10000'), ('USD', '200'), ('XTR', '300')]]}
        status, _, plans[profile] = local.api(operator, '/api/v1/operator/catalogue/plans',
            {'terms': terms, 'reason': 'Owned change acceptance'}, actor['csrf_token'], str(uuid4()))
        assert status == 201
        purchase.private(profile + '-plan.json', plans[profile])
    results = []
    for name in ('active', 'expired', 'exhausted', 'roundtrip', 'late-ban', 'late-source', 'late-identity'):
        opener, login = purchase.signup(name)
        account = login['account']['account_id']
        initial = purchase.create_order(opener, login, plans['regular'])
        paid(initial)
        local.wait_until(lambda: purchase.applied(opener, initial['order_id']), timeout=60)
        allocated = purchase.account_row(account)
        if name in ('expired', 'exhausted'):
            expire_or_exhaust(account, name == 'exhausted')
        before = purchase.panel(account)
        value = change(opener, login, plans['euru'])
        earliest = int(time.time() * 1000)
        if name.startswith('late-'):
            if name == 'late-ban':
                operation(operator, actor, account, {'kind': 'set_vpn_ban', 'vpn_banned': True})
            elif name == 'late-source':
                assigned = operation(operator, actor, account, {'kind': 'assign_plan',
                    'plan_id': plans['regular']['plan_id'], 'revision': plans['regular']['revision'], 'period_days': 30})
                assert assigned['operation_id'] != value['quote']['source_access_operation_id']
                assert local.api(opener, '/api/v1/subscription/plan-change')[2]['current_plan_id'] == plans['regular']['plan_id']
            else:
                panel, csrf = local.login_panel()
                payload = operations.native_update_payload(allocated, native(account)['client'])
                local.panel_call(panel, 'panel/api/clients/update/' + allocated['panel_key'], {**payload, 'id': str(uuid4())}, csrf)
            panel, _ = local.login_panel()
            unchanged = local.panel_call(panel, 'panel/api/clients/get/' + allocated['panel_key'])
            count = purchase.account_row(account)['access_operations']
            paid(value)
            def reviewed():
                current = purchase.order(opener, value['order_id'])
                return current if current['fulfillment_status'] == 'needs_review' else None
            current = local.wait_until(reviewed, timeout=60)
            saved = proof(value)
            assert current['payment_status'] == 'paid' and current['review_required']
            assert saved['receipts'] == 1 and saved['funded'] and saved['operation'] is None
            assert purchase.account_row(account)['access_operations'] == count
            assert local.panel_call(panel, 'panel/api/clients/get/' + allocated['panel_key']) == unchanged
            results.append({'case': name, 'receipt_retained': True, 'review_no_native_write': True})
        else:
            paid(value)
            result = assert_applied(opener, login, value, before, earliest)
            if name == 'roundtrip':
                second = change(opener, login, plans['regular'])
                before_second = purchase.panel(account)
                earliest_second = int(time.time() * 1000)
                paid(second)
                assert_applied(opener, login, second, before_second, earliest_second)
                assert proof(second)['source'] == proof(value)['operation']
            assert purchase.account_row(account)['assigned_panel_id'] == allocated['assigned_panel_id']
            assert purchase.account_row(account)['access_operations'] == (3 if name == 'roundtrip' else 2)
            results.append({'case': name, **result, 'server_preserved': True})
        print('PASS: native change ' + name, flush=True)
    purchase.private('native-report.json', {'panel_version': '3.7.0', 'results': results,
        'real_payment': False, 'live_vpn_changed': False})


def financial_snapshot(db=None):
    value = purchase.financial_snapshot(db)
    value['identity_digest'] = bridge.query("""BEGIN READ ONLY;
      SELECT md5(jsonb_agg(jsonb_build_array(id,vpn_id,sub_id,panel_key,assigned_panel_id) ORDER BY id)::text)
      FROM accounts; ROLLBACK;""", db=db)
    return value


def restore():
    credentials = json.loads((purchase.STATE / 'active-account.json').read_text())
    opener = local.session()
    _, _, login = local.api(opener, '/api/v1/auth/login', {'email': credentials['email'], 'password': credentials['password']})
    plan = json.loads((purchase.STATE / 'regular-plan.json').read_text())
    before = purchase.panel(credentials['account_id'])
    value = change(opener, login, plan)
    earliest = int(time.time() * 1000)
    restored = local.restore_name('plan-change')
    created, paused = False, False
    bridge.query("""CREATE FUNCTION c17_pause_access() RETURNS trigger LANGUAGE plpgsql AS $$
      BEGIN IF NEW.kind='access_operation' THEN NEW.scheduled_at=now()+interval '10 minutes'; END IF;
      RETURN NEW; END $$;
      CREATE TRIGGER c17_pause_access BEFORE INSERT ON river_job FOR EACH ROW EXECUTE FUNCTION c17_pause_access();""")
    try:
        paid(value)
        def prepared():
            saved = proof(value)
            assert saved['fulfillment'] != 'needs_review'
            return saved if saved['operation'] is not None else None
        saved = local.wait_until(prepared, timeout=60)
        assert saved['paid'] and saved['funded'] and saved['target'] and saved['jobs'] == 1
        assert saved['access_status'] != 'applied' and purchase.panel(credentials['account_id']) == before
        compose('stop', 'backend')
        paused = True
        bridge.query('DROP TRIGGER c17_pause_access ON river_job; DROP FUNCTION c17_pause_access();')
        digest = financial_snapshot()
        dump = compose('exec', '-T', 'postgres', 'pg_dump', '-U', local.PG_USER, '-Fc', '-d', local.database())
        path = purchase.STATE / 'prepared-change.dump';path.write_bytes(dump);path.chmod(0o600)
        bridge.query('CREATE DATABASE ' + restored + ';', db='postgres')
        created = True
        compose('exec', '-T', 'postgres', 'pg_restore', '-U', local.PG_USER, '--no-owner', '--no-privileges', '-d', restored, stdin=dump)
        assert financial_snapshot(restored) == digest and proof(value, restored) == saved
        maintenance = (ROOT / 'backend/db/maintenance/post_restore_auth.sql').read_text()
        counts = bridge.query(maintenance, db=restored).splitlines()
        assert len(counts) == 3 and all(c.isdigit() for c in counts)
        assert bridge.query(maintenance, db=restored) == '0\n0\n0'
        assert financial_snapshot(restored) == digest and proof(value, restored) == saved
        purchase.private('restore-report.json', {'prepared_change_preserved': True,
            'source': saved['source'], 'financial_snapshot': digest, 'target_digest': saved['target_digest'],
            'read_only_digest_equal': True, 'auth_maintenance_idempotent': True, 'restored_writers_started': False})
    finally:
        bridge.query('DROP TRIGGER IF EXISTS c17_pause_access ON river_job; DROP FUNCTION IF EXISTS c17_pause_access();')
        if created:
            bridge.query('DROP DATABASE ' + restored + ';', db='postgres')
        if paused:
            bridge.query("UPDATE river_job SET scheduled_at=now() WHERE kind='access_operation' AND args->>'operation_id'=:'operation';", operation=saved['operation'])
            compose('up', '--no-build', '--pull', 'never', '--no-deps', '-d', 'backend')
    local.wait_until(local.ready)
    result = assert_applied(opener, login, value, before, earliest)
    assert proof(value)['target_digest'] == saved['target_digest']
    purchase.private('restart-report.json', {'saved_target_reused': True, **result})
    print('PASS: prepared change restored READ ONLY; restart reused one saved target and native identity')


def main():
    actions = {'prepare': prepare, 'up': up, 'check': check, 'restore': restore, 'stop': lambda: compose('stop')}
    if len(sys.argv) != 2 or sys.argv[1] not in actions:
        raise ValueError('use prepare/up/check/restore/stop')
    actions[sys.argv[1]]()


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        purchase.private('failure.json', {'traceback': traceback.format_exc()})
        frames = ', '.join(Path(f.filename).name + ':' + str(f.lineno) + ':' + f.name for f in traceback.extract_tb(error.__traceback__))
        print('FAIL: own change acceptance ' + type(error).__name__ + ' at ' + frames, file=sys.stderr)
        raise SystemExit(1)
