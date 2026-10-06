"""Owned cabinet-c16 renewal acceptance; signed localhost fixtures only."""
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone
from hashlib import sha256
import importlib.util
import json
import os
from pathlib import Path
import socket
import sqlite3
import subprocess
import sys
import time
import traceback
from urllib.error import HTTPError
from uuid import UUID, uuid4

ROOT = Path(__file__).resolve().parents[2]
STATE = Path(os.environ.get('LOCAL_STATE_DIR', ROOT / '.superpowers/acceptance/c16-renewal/native')).resolve()
assert STATE.is_relative_to(ROOT / '.superpowers/acceptance/c16-renewal'), 'requires own c16 state'
STATE.mkdir(mode=0o700, parents=True, exist_ok=True)
STATE.chmod(0o700)
runtime = STATE / 'runtime.json'
if not runtime.exists():
    runtime.write_text(json.dumps({'project': 'cabinet-c16', 'postgres_user': 'cabinet_c16',
        'base_database': 'cabinet_c16', 'fixture_prefixes': {'purchase': 'c16-purchase-'}}))
    runtime.chmod(0o600)
os.environ['LOCAL_STATE_DIR'] = str(STATE)
spec = importlib.util.spec_from_file_location('c16_purchase_fixture', ROOT / 'deploy/purchase/local.py')
purchase = importlib.util.module_from_spec(spec)
spec.loader.exec_module(purchase)
local, bridge = purchase.local, purchase.bridge
assert local.PROFILE == 'native' and local.PROJECT == 'cabinet-c16'
assert local.PG_USER == local.BASE_DATABASE == 'cabinet_c16'
purchase.STATE, bridge.STATE = STATE / 'purchase', STATE / 'roles'


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
    config.update(APP_NETWORK_SUBNET='10.253.16.0/28', APP_GATEWAY_IP='10.253.16.14',
        BOT_OPERATOR_IDS='', TELEGRAM_ENABLED='false')
    local.write('public.env', '\n'.join(k + '=' + v for k, v in config.items()) + '\n')
    revision = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    label = {'org.opencontainers.image.revision': revision}
    backend = {'image': 'cabinet-c16-backend:local', 'build': {'labels': label}}
    gateway = {'image': 'cabinet-c16-web:local', 'build': {'labels': label}}
    local.write('images.json', json.dumps({'services': {'backend': backend, 'migrate': backend,
        'reconcile': backend, 'gateway': gateway, 'origin': {'image': gateway['image']}}}))
    purchase.prepare()
    configured = json.loads(compose('config', '--format', 'json'))
    env = configured['services']['backend']['environment']
    assert env['LEGACY_BOT_API_ENABLED'] == 'false' and env['TELEGRAM_ENABLED'] == 'false'
    assert configured['services']['panel']['image'] == 'ghcr.io/mhsanaei/3x-ui:3.7.0@sha256:3b3131f1876e6bf35063a9ec4dd1c594e4525180bfc2e1c477dcc8a3c9550ca1'
    purchase.private('image-source.json', {'revision': revision})
    print('PASS: own c16 identity, local pinned3.7.0, file credentials and synthetic YooMoney')


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
    source = json.loads((purchase.STATE / 'image-source.json').read_text())['revision']
    images = json.loads(local.command(['docker', 'image', 'inspect', *[c['Image'] for c in containers]]))
    assert all(c['Config']['Labels']['com.docker.compose.project'] == 'cabinet-c16' for c in containers)
    assert all(i['Config']['Labels']['org.opencontainers.image.revision'] == source for i in images[:2])
    assert containers[0]['Config']['Cmd'] == ['serve']
    purchase.private('images.json', {'revision': source, 'ids': [c['Image'] for c in containers],
        'one_backend': True, 'native_version': '3.7.0'})
    print('PASS: own Go/web source labels and native3.7.0; one backend, no external provider')


def native(account):
    row = purchase.account_row(account)
    opener, _ = local.login_panel()
    value = local.panel_call(opener, 'panel/api/clients/get/' + row['panel_key'])
    assert value['client']['uuid'] == row['vpn_id'] and value['client']['subId'] == row['sub_id']
    assert isinstance(value['usedTraffic'], int)
    return value


def update_payload(account, client):
    helper = importlib.util.spec_from_file_location('renewal_native_payload', ROOT / 'deploy/subscription-operations/local.py')
    module = importlib.util.module_from_spec(helper)
    helper.loader.exec_module(module)
    return module.native_update_payload(purchase.account_row(account), client)


def expire_or_exhaust(account, exhausted):
    row = purchase.account_row(account)
    before = native(account)
    opener, csrf = local.login_panel()
    if not exhausted:
        local.panel_call(opener, 'panel/api/clients/update/' + row['panel_key'],
            {**update_payload(account, before['client']), 'expiryTime': int(time.time() * 1000) - 86400000}, csrf)
    local.panel_call(opener, 'panel/api/clients/bulkDisable', {'emails': [row['panel_key']]}, csrf)
    if exhausted:
        # Synthetic counters only, while this project's panel writer is stopped.
        compose('stop', 'panel')
        try:
            with sqlite3.connect(STATE / 'panel-db/x-ui.db') as db:
                assert db.execute('SELECT count(*) FROM clients WHERE email=? AND uuid=? AND sub_id=?',
                    (row['panel_key'], row['vpn_id'], row['sub_id'])).fetchone()[0] == 1
                total = before['client']['totalGB']
                assert total > 0
                changed = db.execute('UPDATE client_traffics SET up=?,down=0 WHERE email=?',
                    (total + 1, row['panel_key'])).rowcount
                assert changed == len(before['inboundIds']) and changed > 0
        finally:
            compose('up', '--no-build', '--pull', 'never', '--no-deps', '-d', 'panel')
        def panel_ready():
            try:
                return local.login_panel()
            except HTTPError as error:
                if error.code in (502, 503):
                    return None
                raise
            except OSError:
                return None
        local.wait_until(panel_ready)
    after = native(account)
    assert after['client']['enable'] is False
    assert after['client']['uuid'] == before['client']['uuid']
    assert after['client']['subId'] == before['client']['subId'] and after['inboundIds'] == before['inboundIds']
    if exhausted:
        assert after['usedTraffic'] >= after['client']['totalGB']
        assert after['client']['expiryTime'] == before['client']['expiryTime'] > int(time.time() * 1000)
    else:
        assert after['client']['expiryTime'] < int(time.time() * 1000)


def renew(opener, login):
    plan = local.api(opener, '/api/v1/subscription/renewal')[2]
    body = {'action': 'renew', 'plan_id': plan['plan_id'], 'revision': plan['revision'],
        'period_days': 30, 'payment_method': 'yoomoney', 'payment_type': 'PC'}
    key = str(uuid4())
    status, _, value = local.api(opener, '/api/v1/orders', body, login['csrf_token'], key)
    assert status == 201 and value['action'] == 'renew' and value['quote']['amount_minor'] == '10000'
    assert value['checkout']['fields']['sum'] == '100.00' and value['can_pay']
    assert local.api(opener, '/api/v1/orders', body, login['csrf_token'], key)[2]['order_id'] == value['order_id']
    assert purchase.manual_api(opener, '/api/v1/orders', {**body, 'payment_type': 'AC'}, login['csrf_token'], key)[0] == 409
    assert local.api(opener, '/api/v1/orders/current')[2]['order']['order_id'] == value['order_id']
    return value


def paid(opener, value):
    status, receipt = purchase.notification(value)
    assert status == 200
    with ThreadPoolExecutor(max_workers=2) as executor:
        results = list(executor.map(lambda _: purchase.notification(value, receipt=receipt), range(2)))
    assert all(result[0] == 200 for result in results)
    purchase.private('notice-' + value['order_id'] + '.json', {'receipt': receipt, 'fields': purchase.NOTICES[receipt]})
    return receipt


def proof(order, db=None):
    return json.loads(bridge.query("""BEGIN READ ONLY;
      SELECT json_build_object('action',p.action,'paid',p.payment_status='paid',
        'funded',p.funding_operation_id IS NOT NULL,'fulfillment',p.fulfillment_status,
        'operation',p.access_operation_id,'target',a.target,'steps',a.completed_steps,
        'reset_started',a.reset_started,'access_status',a.status,
        'receipts',(SELECT count(*) FROM purchase_receipts r WHERE r.order_id=p.id),
        'jobs',(SELECT count(*) FROM river_job j WHERE j.kind='access_operation'
          AND j.args->>'operation_id'=a.id::text),'target_digest',md5(a.target::text))
      FROM purchase_orders p LEFT JOIN access_operations a ON a.id=p.access_operation_id
      WHERE p.id=:'order'::uuid; ROLLBACK;""", db=db, order=order['order_id']))


def assert_applied(opener, login, value, before, earliest):
    result = local.wait_until(lambda: purchase.applied(opener, value['order_id']), timeout=60)
    assert result['payment_status'] == 'paid' and result['action'] == 'renew'
    account = login['account']['account_id']
    after = purchase.panel(account)
    for field in ('identity_digest', 'limit_ip', 'traffic_bytes', 'memberships'):
        assert before[field] == after[field], field
    assert after['enabled'] and native(account)['usedTraffic'] == 0
    duration = 30 * 86400000
    if before['expiry_ms'] > earliest:
        assert after['expiry_ms'] == before['expiry_ms'] + duration
    else:
        assert earliest + duration <= after['expiry_ms'] <= int(time.time() * 1000) + duration
    saved = proof(value)
    assert saved['paid'] and saved['funded'] and saved['action'] == 'renew'
    assert saved['receipts'] == saved['jobs'] == 1 and saved['access_status'] == 'applied'
    assert saved['target']['expiry_time_ms'] == after['expiry_ms'] and saved['target']['reset']
    receipt = json.loads((purchase.STATE / ('notice-' + value['order_id'] + '.json')).read_text())['receipt']
    assert purchase.notification(value, receipt=receipt)[0] == 200
    assert proof(value) == saved and purchase.panel(account) == after
    return {'applied': True, 'single_target_receipt_job': True, 'replay_unchanged': True,
        'identity_preserved': True, 'reset_zero': True, 'target_digest': saved['target_digest']}


def operation(operator, actor, account, body):
    path = '/api/v1/operator/clients/' + purchase.owned(account) + '/access-operations'
    status, _, value = local.api(operator, path, {**body, 'reason': 'Owned synthetic renewal guard'},
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
    devices = int(bridge.query("SELECT min(n) FROM generate_series(3,10000) n WHERE NOT EXISTS(SELECT 1 FROM catalogue_plans WHERE NOT archived AND current_devices=n);"))
    terms = {'devices': devices, 'traffic_gb': 30, 'profile': 'regular', 'hidden': False,
        'periods': [30], 'prices': [{'period_days': 30, 'currency': currency, 'amount_minor': minor}
            for currency, minor in [('RUB', '10000'), ('USD', '200'), ('XTR', '300')]]}
    status, _, plan = local.api(operator, '/api/v1/operator/catalogue/plans',
        {'terms': terms, 'reason': 'Owned renewal acceptance'}, actor['csrf_token'], str(uuid4()))
    assert status == 201
    purchase.private('plan.json', plan)
    report = []
    for name in ('active', 'expired', 'exhausted', 'late-ban', 'late-plan', 'late-identity', 'trial-first-purchase'):
        opener, login = purchase.signup(name)
        account = login['account']['account_id']
        initial = purchase.create_order(opener, login, plan)
        paid(opener, initial)
        local.wait_until(lambda: purchase.applied(opener, initial['order_id']), timeout=60)
        allocated = purchase.account_row(account)
        if name == 'trial-first-purchase':
            operation(operator, actor, account, {'kind': 'starter_trial'})
            assert purchase.manual_api(opener, '/api/v1/subscription/renewal')[0] == 409
            assert local.api(opener, '/api/v1/orders/current')[2]['can_purchase'] is True
            value = purchase.create_order(opener, login, plan)
            paid(opener, value)
            result = local.wait_until(lambda: purchase.applied(opener, value['order_id']), timeout=60)
            assert result['action'] == 'purchase' and not result['review_required']
            saved = proof(value)
            after = purchase.panel(account)
            assert saved['paid'] and saved['funded'] and saved['action'] == 'purchase'
            assert saved['receipts'] == saved['jobs'] == 1 and saved['access_status'] == 'applied'
            assert saved['target']['reset'] and native(account)['usedTraffic'] == 0
            assert after['enabled'] and after['limit_ip'] == terms['devices']
            assert after['traffic_bytes'] == terms['traffic_gb'] * 1024**3
            assert purchase.account_row(account)['assigned_panel_id'] == allocated['assigned_panel_id']
            assert native(account)['client']['uuid'] == allocated['vpn_id']
            assert native(account)['client']['subId'] == allocated['sub_id']
            receipt = json.loads((purchase.STATE / ('notice-' + value['order_id'] + '.json')).read_text())['receipt']
            assert purchase.notification(value, receipt=receipt)[0] == 200
            assert proof(value) == saved and purchase.panel(account) == after
            assert local.api(opener, '/api/v1/orders/current')[2]['can_purchase'] is False
            assert local.api(opener, '/api/v1/subscription/renewal')[2]['plan_id'] == plan['plan_id']
            report.append({'case': name, 'purchase_after_clearing': True, 'identity_preserved': True,
                'single_target_receipt_job': True, 'replay_unchanged': True, 'reset_zero': True,
                'next_purchase_blocked': True, 'normal_renewal_restored': True})
            print('PASS: native renewal ' + name)
            continue
        if name in ('expired', 'exhausted'):
            expire_or_exhaust(account, name == 'exhausted')
        before = purchase.panel(account)
        value = renew(opener, login)
        if name.startswith('late-'):
            if name == 'late-ban':
                operation(operator, actor, account, {'kind': 'set_vpn_ban', 'vpn_banned': True})
            elif name == 'late-plan':
                status, _, other = local.api(operator, '/api/v1/operator/catalogue/plans',
                    {'terms': {**terms, 'devices': devices + 1}, 'reason': 'Owned different plan'},
                    actor['csrf_token'], str(uuid4()))
                assert status == 201
                operation(operator, actor, account, {'kind': 'assign_plan', 'plan_id': other['plan_id'],
                    'revision': other['revision'], 'period_days': 30})
            else:
                client = native(account)['client']
                panel, csrf = local.login_panel()
                local.panel_call(panel, 'panel/api/clients/update/' + allocated['panel_key'],
                    {**update_payload(account, client), 'id': str(uuid4())}, csrf)
            panel, _ = local.login_panel()
            unchanged = local.panel_call(panel, 'panel/api/clients/get/' + allocated['panel_key'])
            operations = purchase.account_row(account)['access_operations']
            if name != 'late-identity':
                assert not purchase.order(opener, value['order_id'])['can_pay']
            paid(opener, value)
            def reviewed():
                current = purchase.order(opener, value['order_id'])
                return current if current['fulfillment_status'] == 'needs_review' else None
            reviewed_value = local.wait_until(reviewed, timeout=60)
            assert reviewed_value['payment_status'] == 'paid' and reviewed_value['review_required']
            saved = proof(value)
            assert saved['receipts'] == 1 and saved['funded'] and saved['operation'] is None
            assert purchase.account_row(account)['access_operations'] == operations
            assert local.panel_call(panel, 'panel/api/clients/get/' + allocated['panel_key']) == unchanged
            report.append({'case': name, 'receipt_retained': True, 'review_no_native_write': True})
        else:
            earliest = int(time.time() * 1000)
            paid(opener, value)
            result = assert_applied(opener, login, value, before, earliest)
            assert purchase.account_row(account)['assigned_panel_id'] == allocated['assigned_panel_id']
            assert purchase.account_row(account)['access_operations'] == 2
            next_order = renew(opener, login)
            assert local.api(opener, '/api/v1/orders/' + next_order['order_id'] + '/cancel',
                {}, login['csrf_token'], str(uuid4()))[0] == 200
            report.append({'case': name, **result, 'next_renewal_permitted': True, 'server_preserved': True})
        print('PASS: native renewal ' + name)
    purchase.private('native-report.json', {'panel_version': '3.7.0', 'results': report,
        'real_payment': False, 'live_vpn_changed': False})


def financial_snapshot(db=None):
    return json.loads(bridge.query("""BEGIN READ ONLY; SELECT json_build_object(
      'orders',(SELECT count(*) FROM purchase_orders),
      'renewals',(SELECT count(*) FROM purchase_orders WHERE action='renew'),
      'receipts',(SELECT count(*) FROM purchase_receipts),
      'digest',md5(jsonb_build_object(
        'orders',(SELECT jsonb_agg(to_jsonb(o) ORDER BY id) FROM purchase_orders o),
        'receipts',(SELECT jsonb_agg(to_jsonb(r) ORDER BY operation_id) FROM purchase_receipts r),
        'access',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM access_operations a),
        'identity',(SELECT jsonb_agg(jsonb_build_array(id,vpn_id,sub_id,panel_key,assigned_panel_id)
          ORDER BY id) FROM accounts))::text)); ROLLBACK;""", db=db))


def restore():
    credentials = json.loads((purchase.STATE / 'active-account.json').read_text())
    opener = local.session()
    _, _, login = local.api(opener, '/api/v1/auth/login',
        {'email': credentials['email'], 'password': credentials['password']})
    before = purchase.panel(credentials['account_id'])
    value = renew(opener, login)
    earliest = int(time.time() * 1000)
    restored = local.restore_name('renewal')
    created, paused = False, False
    bridge.query("""CREATE FUNCTION c16_pause_access() RETURNS trigger LANGUAGE plpgsql AS $$
      BEGIN IF NEW.kind='access_operation' THEN NEW.scheduled_at=now()+interval '10 minutes'; END IF;
      RETURN NEW; END $$;
      CREATE TRIGGER c16_pause_access BEFORE INSERT ON river_job FOR EACH ROW EXECUTE FUNCTION c16_pause_access();""")
    try:
        paid(opener, value)
        def prepared():
            saved = proof(value)
            assert saved['fulfillment'] != 'needs_review'
            return saved if saved['operation'] is not None else None
        saved = local.wait_until(prepared, timeout=60)
        assert saved['paid'] and saved['funded'] and saved['target'] and saved['jobs'] == 1
        assert saved['access_status'] != 'applied' and purchase.panel(credentials['account_id']) == before
        compose('stop', 'backend')
        paused = True
        bridge.query('DROP TRIGGER c16_pause_access ON river_job; DROP FUNCTION c16_pause_access();')
        digest = financial_snapshot()
        dump = compose('exec', '-T', 'postgres', 'pg_dump', '-U', local.PG_USER, '-Fc', '-d', local.database())
        path = purchase.STATE / 'prepared-renewal.dump'
        path.write_bytes(dump)
        path.chmod(0o600)
        bridge.query('CREATE DATABASE ' + restored + ';', db='postgres')
        created = True
        compose('exec', '-T', 'postgres', 'pg_restore', '-U', local.PG_USER,
            '--no-owner', '--no-privileges', '-d', restored, stdin=dump)
        assert financial_snapshot(restored) == digest and proof(value, restored) == saved
        maintenance = (ROOT / 'backend/db/maintenance/post_restore_auth.sql').read_text()
        counts = bridge.query(maintenance, db=restored).splitlines()
        assert len(counts) == 3 and all(c.isdigit() for c in counts)
        assert bridge.query(maintenance, db=restored) == '0\n0\n0'
        assert financial_snapshot(restored) == digest and proof(value, restored) == saved
        purchase.private('restore-report.json', {'prepared_renewal_preserved': True,
            'financial_snapshot': digest, 'target_digest': saved['target_digest'],
            'read_only_digest_equal': True, 'auth_maintenance_idempotent': True,
            'restored_writers_started': False})
    finally:
        bridge.query('DROP TRIGGER IF EXISTS c16_pause_access ON river_job; DROP FUNCTION IF EXISTS c16_pause_access();')
        if created:
            bridge.query('DROP DATABASE ' + restored + ';', db='postgres')
        if paused:
            bridge.query("UPDATE river_job SET scheduled_at=now() WHERE kind='access_operation' AND args->>'operation_id'=:'operation';",
                operation=saved['operation'])
            compose('up', '--no-build', '--pull', 'never', '--no-deps', '-d', 'backend')
    local.wait_until(local.ready)
    result = assert_applied(opener, login, value, before, earliest)
    assert proof(value)['target_digest'] == saved['target_digest']
    purchase.private('restart-report.json', {'saved_target_reused': True, **result})
    print('PASS: prepared renewal restored READ ONLY; restart reused one saved target and native identity')


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
        frames = ', '.join(Path(f.filename).name + ':' + str(f.lineno) + ':' + f.name
                           for f in traceback.extract_tb(error.__traceback__))
        print('FAIL: own renewal acceptance ' + type(error).__name__ + ' at ' + frames, file=sys.stderr)
        raise SystemExit(1)
