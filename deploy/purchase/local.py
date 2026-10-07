"""Owned localhost purchase acceptance; synthetic signed callbacks, never sends money."""
from datetime import datetime, timezone
from hashlib import sha256
import hmac
import importlib.util
import json
import os
from pathlib import Path
import re
import secrets
import sys
import time
from urllib.error import HTTPError
from urllib.parse import quote, urlencode, urlsplit, urlunsplit
import urllib.request
from uuid import UUID, uuid4

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('purchase_restrictions', ROOT / 'deploy/account-restrictions/local.py')
bridge = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bridge)
local = bridge.local
STATE = local.acceptance_state('purchase')
NOTICES = {}


def private(name, value):
    if not re.fullmatch(r'[a-z][a-z0-9.-]*', name):
        raise ValueError('invalid private artifact name')
    STATE.mkdir(mode=0o700, parents=True, exist_ok=True)
    STATE.chmod(0o700)
    path = STATE / name
    with path.open('w') as output:
        output.write(json.dumps(value) + '\n')
    path.chmod(0o600)
    return path


def compose(*args, stdin=None):
    # Same owned resource identity and declarations as the original stack.
    config = dict(line.split('=', 1) for line in local.ENV.read_text().splitlines()
                  if line and not line.startswith('#'))
    return local.command(['docker', 'compose', '--project-name', local.PROJECT,
                          '--profile', 'restore', '--env-file', str(local.ENV),
                          '-f', 'deploy/acceptance/compose.acceptance.yml',
                          '-f', 'deploy/acceptance/compose.local.yml',
                          *(['-f', 'deploy/acceptance/compose.native.yml'] if local.PROFILE == 'native' else []),
                          '-f', 'deploy/purchase/compose.yoomoney.yml',
                          *(['-f', 'deploy/purchase/compose.manual.yml']
                            if config.get('SHOP_PAYMENT_MANUAL_ENABLED') == 'true' else []),
                          *args], stdin=stdin)


def prepare():
    if not local.ENV.is_file() or not (local.STATE / 'runtime.json').is_file():
        raise RuntimeError('start the owned local acceptance stack first')
    config = dict(line.split('=', 1) for line in local.ENV.read_text().splitlines()
                  if line and not line.startswith('#'))
    if config.get('CABINET_ORIGIN') != local.ORIGIN or config.get('CABINET_HOST') != 'localhost':
        raise RuntimeError('purchase acceptance requires the owned localhost origin')
    secret = local.STATE / 'yoomoney-notification-secret'
    if not secret.exists():
        fd = os.open(secret, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, 'w') as output:
            output.write(secrets.token_urlsafe(32) + '\n')
    config.update(SHOP_PAYMENT_YOOMONEY_ENABLED='true', YOOMONEY_WALLET_ID='410000000000000',
                  YOOMONEY_NOTIFICATION_SECRET_FILE=str(secret))
    local.write('public.env', '\n'.join(k + '=' + v for k, v in config.items()) + '\n')
    configured = json.loads(compose('config', '--format', 'json'))
    if local.PROFILE == 'native' and configured['services']['backend']['environment'].get('LEGACY_BOT_API_ENABLED') != 'false':
        raise RuntimeError('native purchase requires disabled legacy bot API')
    print('PASS: owned localhost payment overlay; disposable secret, no provider requests')


def prepare_manual():
    prepare()
    details = local.STATE / 'manual-card-details'
    if not details.exists():
        local.write('manual-card-details', 'Synthetic local recipient only\nReference: your order ID\n')
    assert details.stat().st_mode & 0o777 == 0o600, 'unprotected manual fixture details'
    config = dict(line.split('=', 1) for line in local.ENV.read_text().splitlines()
                  if line and not line.startswith('#'))
    config.update(SHOP_PAYMENT_MANUAL_ENABLED='true', MANUAL_CARD_DETAILS_FILE=str(details))
    local.write('public.env', '\n'.join(k + '=' + v for k, v in config.items()) + '\n')
    configured = json.loads(compose('config', '--format', 'json'))
    assert configured['services']['backend']['environment']['SHOP_PAYMENT_MANUAL_ENABLED'] == 'true'
    print('PASS: own manual overlay, synthetic instructions, no bank or provider request')


def owned(account):
    account = str(UUID(account))
    raw = bridge.query("""SELECT a.kind='web' AND
        (a.email_key LIKE :'fixture_pattern' OR a.email_key LIKE :'new_fixture_pattern')
        FROM accounts a WHERE a.id=:'account'::uuid;""", account=account,
                       **bridge.fixture_patterns('purchase'))
    if raw != 't':
        raise RuntimeError('purchase fixture ownership check failed')
    return account


def account_row(account):
    account = owned(account)
    return json.loads(bridge.query("""SELECT json_build_object(
        'vpn_id',vpn_id,'sub_id',sub_id,'panel_key',panel_key,'assigned_panel_id',assigned_panel_id,
        'grants',(SELECT count(*) FROM trial_grants g JOIN trial_operations t ON t.id=g.operation_id
          WHERE t.account_id=a.id),'access_operations',(SELECT count(*) FROM access_operations x WHERE x.account_id=a.id))
        FROM accounts a WHERE id=:'account'::uuid;""", account=account))


def panel(account):
    row = account_row(account)
    opener, _ = local.login_panel()
    _, _, raw = local.request(opener, 'https://localhost:59444/panel/api/clients/get/' + row['panel_key'])
    result = json.loads(raw)
    if result.get('success') is False and result.get('obj') is None:
        return {'exists': False}
    if result.get('success') is not True:
        raise RuntimeError('native readback unavailable')
    record = result['obj']['client']
    assert record['uuid'] == row['vpn_id'] and record['subId'] == row['sub_id'], 'native identity differs'
    return {'exists': True, 'identity_digest': sha256(json.dumps(
        [row['vpn_id'], row['sub_id'], row['panel_key']], separators=(',', ':')).encode()).hexdigest(),
        'expiry_ms': record['expiryTime'], 'limit_ip': record['limitIp'],
        'traffic_bytes': record['totalGB'], 'enabled': record['enable'],
        'memberships': sorted(result['obj']['inboundIds'])}


def signup(name):
    opener = local.session()
    email = local.fixture_prefix('purchase') + name + '-' + uuid4().hex + '@example.test'
    status, _, _ = local.api(opener, '/api/v1/auth/register', {
        'email': email, 'locale': 'en', 'accepted_terms_version': '1', 'accepted_privacy_version': '1'})
    assert status == 202, 'registration failed'
    token = local.wait_until(lambda: local.mail_token(email))
    password = 'Local purchase ' + secrets.token_urlsafe(20)
    assert local.api(opener, '/api/v1/auth/verify-email', {'token': token, 'new_password': password})[0] == 200
    _, _, login = local.api(opener, '/api/v1/auth/login', {'email': email, 'password': password})
    account = owned(login['account']['account_id'])
    private(name + '-account.json', {'email': email, 'password': password, 'account_id': account})
    return opener, login


def notification(order, *, receipt=None, test=False, tamper=False):
    order_id = str(UUID(order['order_id']))
    amount = int(order['quote']['amount_minor'])
    assert 100 <= amount <= 1000000, 'owned synthetic price outside acceptance bounds'
    fields = {'notification_type': 'card-incoming', 'operation_id': receipt or 'local-' + uuid4().hex,
              'amount': f'{(amount - 1) // 100}.{(amount - 1) % 100:02}',
              'withdraw_amount': f'{amount // 100}.{amount % 100:02}', 'currency': '643',
              'datetime': datetime.now(timezone.utc).isoformat(), 'sender': '', 'codepro': 'false',
              'unaccepted': 'false', 'label': order_id}
    if test:
        fields['test_notification'] = 'true'
    if receipt:
        saved = NOTICES[receipt]
        assert saved['label'] == order_id, 'receipt belongs to another fixture'
        fields = dict(saved)
    elif not test and not tamper:
        NOTICES[fields['operation_id']] = dict(fields)
    canonical = '&'.join(k + '=' + quote(v, safe='~') for k, v in sorted(fields.items()))
    secret = (local.STATE / 'yoomoney-notification-secret').read_text().strip()
    fields['sign'] = hmac.new(secret.encode(), canonical.encode(), sha256).hexdigest()
    if tamper:
        fields['withdraw_amount'] = '999.99'
    req = urllib.request.Request(local.ORIGIN + '/webhooks/yoomoney',
          data=urlencode(fields).encode(), headers={'Content-Type': 'application/x-www-form-urlencoded'})
    try:
        with local.session().open(req, timeout=15) as response:
            return response.status, fields['operation_id']
    except HTTPError as error:
        return error.code, fields['operation_id']


def order(opener, order_id):
    return local.api(opener, '/api/v1/orders/' + str(UUID(order_id)))[2]


def applied(opener, order_id):
    value = order(opener, order_id)
    if value['fulfillment_status'] == 'needs_review':
        raise RuntimeError('purchase fulfillment needs review')
    return value if value['fulfillment_status'] == 'applied' else None


def create_order(opener, login, plan, manual=False):
    body = {'action': 'purchase', 'plan_id': plan['plan_id'], 'revision': plan['revision'],
            'period_days': 30, 'payment_method': 'manual' if manual else 'yoomoney',
            'payment_type': 'MANUAL' if manual else 'AC'}
    key = str(uuid4())
    status, _, value = local.api(opener, '/api/v1/orders', body, login['csrf_token'], key)
    assert status == 201 and value['quote']['amount_minor'] == '10000', 'server quote differs'
    if manual:
        assert value['checkout'] is None and value['manual_payment']['can_report']
        assert value['manual_payment']['instructions'] == (local.STATE / 'manual-card-details').read_text().strip()
    else:
        assert value['checkout']['action'] == 'https://yoomoney.ru/quickpay/confirm', 'checkout action differs'
        assert value['checkout']['fields']['sum'] == '100.00', 'checkout decimal differs'
    replay = local.api(opener, '/api/v1/orders', body, login['csrf_token'], key)[2]
    assert replay['order_id'] == value['order_id'], 'lost response duplicated order'
    assert order(opener, value['order_id'])['payment_status'] == 'pending', 'browser return changed payment'
    return value


def exhaust_counters(account, total, memberships):
    """Synthetic exhausted access in this owned panel's Docker VM, with its writer stopped."""
    assert local.PROFILE == 'native' and type(total) is int and total > 0
    assert type(memberships) is int and memberships > 0
    row = account_row(account)
    compose('stop', 'panel')
    try:
        # SQLite WAL must stay in the Docker VM; a Mac connection can see stale state.
        script = '''import json,sqlite3,sys
row,total,memberships=json.load(sys.stdin)
assert total>0
with sqlite3.connect('/panel/x-ui.db') as db:
    assert db.execute('SELECT count(*) FROM clients WHERE email=? AND uuid=? AND sub_id=? AND enable=0',
        (row['panel_key'],row['vpn_id'],row['sub_id'])).fetchone()[0]==1
    changed=db.execute('UPDATE client_traffics SET up=?,down=0 WHERE email=?',
        (total+1,row['panel_key'])).rowcount
    assert changed==memberships and changed>0
'''
        image = (ROOT / 'deploy/acceptance/Dockerfile.bot').read_text().splitlines()[0].split()[1]
        local.command(['docker', 'run', '--rm', '--pull', 'missing', '--network', 'none',
            '--read-only', '--user', str(os.getuid()) + ':' + str(os.getgid()), '-i',
            '--mount', 'type=bind,source=' + str(local.STATE / 'panel-db') + ',target=/panel',
            '--entrypoint', 'python', image, '-c', script],
            stdin=json.dumps([row, total, memberships]).encode())
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


def manual_api(*args):
    try:
        return local.api(*args)
    except HTTPError as error:
        return error.code, error.headers, None


def manual_decision(operator, actor, login, purchase, *, key=None, decision='approve'):
    body = {'decision': decision, 'reason': 'Synthetic fixture funds checked; no bank transfer'}
    if decision == 'approve':
        body['confirmed_amount_minor'] = purchase['quote']['amount_minor']
    path = '/api/v1/operator/clients/' + owned(login['account']['account_id']) + '/orders/' + str(UUID(purchase['order_id'])) + '/manual-decision'
    return manual_api(operator, path, body, actor['csrf_token'], key or str(uuid4()))


def manual_report(opener, login, purchase):
    path = '/api/v1/orders/' + str(UUID(purchase['order_id'])) + '/manual-report'
    key = str(uuid4())
    first = local.api(opener, path, {}, login['csrf_token'], key)
    assert first[0] == 200 and first[2]['manual_payment']['state'] == 'pending'
    assert local.api(opener, path, {}, login['csrf_token'], key)[0] == 200
    raw = bridge.query("""SELECT json_build_object('receipts',
        (SELECT count(*) FROM purchase_receipts WHERE order_id=:'order'::uuid),
        'jobs',(SELECT count(*) FROM river_job WHERE kind='purchase_fulfillment'
          AND args->>'order_id'=:'order'));""", order=purchase['order_id'])
    assert json.loads(raw) == {'receipts': 0, 'jobs': 0}, 'report confirmed money or provisioned'
    assert manual_api(opener, '/api/v1/orders/' + purchase['order_id'] + '/cancel', {}, login['csrf_token'], str(uuid4()))[0] == 409


def check(manual=False):
    local.wait_until(local.ready)
    prefix = 'manual-' if manual else ''
    operator, actor = signup(prefix + 'operator')
    assert bridge.role('grant', actor['account']['account_id']), 'local operator grant failed'
    devices = int(bridge.query("""SELECT min(n) FROM generate_series(3,10000) n
        WHERE NOT EXISTS(SELECT 1 FROM catalogue_plans WHERE NOT archived AND current_devices=n);"""))
    terms = {'devices': devices, 'traffic_gb': 30, 'profile': 'regular', 'hidden': False,
             'periods': [30], 'prices': [{'period_days': 30, 'currency': currency, 'amount_minor': minor}
              for currency, minor in [('RUB', '10000'), ('USD', '200'), ('XTR', '300')]]}
    status, _, plan = local.api(operator, '/api/v1/operator/catalogue/plans',
        {'terms': terms, 'reason': 'Owned purchase acceptance'}, actor['csrf_token'], str(uuid4()))
    assert status == 201 and plan['devices'] == devices, 'owned plan creation failed'
    private(prefix + 'plan.json', plan)
    results = []
    for name in ['new', 'trial']:
        opener, login = signup(prefix + name)
        account = login['account']['account_id']
        before = None
        if name == 'trial':
            _, _, trial = local.api(opener, '/api/v1/trial-requests',
                {'comment': 'Owned purchase conversion'}, login['csrf_token'], str(uuid4()))
            _, _, _ = local.api(operator, '/api/v1/operator/trial-requests/' + trial['request_id'] + '/decision',
                {'decision': 'approve', 'reason': ''}, actor['csrf_token'], str(uuid4()))
            local.wait_until(lambda: local.active(opener))
            before = panel(account)
        purchase = create_order(opener, login, plan, manual)
        if manual:
            assert manual_decision(operator, actor, login, purchase)[0] == 409, 'unreported payment approved'
            assert manual_decision(opener, login, login, purchase)[0] == 403, 'customer approved own money'
            assert manual_api(operator, '/api/v1/orders/' + purchase['order_id'] + '/manual-report', {}, actor['csrf_token'], str(uuid4()))[0] == 404, 'foreign report permitted'
            manual_report(opener, login, purchase)
            queue = local.api(operator, '/api/v1/operator/manual-payments')[2]
            assert any(item['order']['order_id'] == purchase['order_id'] for item in queue['items'])
            receipt = str(uuid4())  # Replay the decision, not a fabricated bank notification.
            assert manual_decision(operator, actor, login, purchase, key=receipt)[0] == 202
        else:
            assert notification(purchase, test=True)[0] == 200
            assert notification(purchase, tamper=True)[0] == 403
            assert order(opener, purchase['order_id'])['payment_status'] == 'pending', 'invalid notice issued access'
            code, receipt = notification(purchase)
            assert code == 200, 'signed local notice rejected'
        final = local.wait_until(lambda: applied(opener, purchase['order_id']), timeout=60)
        after = panel(account)
        assert after['enabled'] and after['limit_ip'] == devices + 1 and after['traffic_bytes'] == 30 * 1024 ** 3
        if before:
            assert before['identity_digest'] == after['identity_digest'], 'conversion changed native identity'
            assert after['expiry_ms'] == before['expiry_ms'] + 30 * 86400000, 'conversion lost finite trial time'
            assert account_row(account)['grants'] == 1, 'conversion removed trial grant'
        else:
            assert account_row(account)['grants'] == 0, 'purchase manufactured trial grant'
            assert abs(after['expiry_ms'] - int(time.time() * 1000) - 30 * 86400000) < 60000
        if manual:
            assert manual_decision(operator, actor, login, purchase, key=receipt)[0] == 202
            assert manual_decision(operator, actor, login, purchase, decision='reject')[0] == 409
        else:
            assert notification(purchase, receipt=receipt)[0] == 200
        assert panel(account) == after, 'receipt replay changed access'
        subscription = local.api(opener, '/api/v1/subscription')[2]
        assert subscription['status'] == 'active' and subscription['devices'] == devices
        status, _, key = local.api(opener, '/api/v1/subscription/key')
        assert status == 200 and key, 'paid subscription key unavailable'
        private(prefix + name + '-order.json', final)
        results.append({'fixture': name, 'paid': final['payment_status'] == 'paid',
                        'applied': final['fulfillment_status'] == 'applied',
                        'one_access_operation': account_row(account)['access_operations'] == 1,
                        'preserved_trial': before is not None, 'repeat_unchanged': True})
    if manual:
        opener, login = signup('manual-rejected')
        purchase = create_order(opener, login, plan, True)
        manual_report(opener, login, purchase)
        assert manual_decision(operator, actor, login, purchase, decision='reject')[0] == 200
        rejected = order(opener, purchase['order_id'])
        assert rejected['payment_status'] == 'canceled' and rejected['manual_payment']['reason']
        assert manual_decision(operator, actor, login, purchase)[0] == 409
        create_order(opener, login, plan, True)
        assert not panel(login['account']['account_id'])['exists'], 'rejection provisioned access'
    private(prefix + 'native-report.json', {'panel_version': '3.7.0', 'results': results,
                                  'real_payment': False, 'live_vpn_changed': False})
    assert all(r['paid'] and r['applied'] and r['one_access_operation'] for r in results)
    print('PASS: ' + ('synthetic operator-confirmed manual' if manual else 'signed local') +
          ' payment -> real 3X-UI 3.7.0 for new and trial accounts; repeat unchanged')


def financial_snapshot(db=None):
    # No provider ID, account, money or panel identifiers are printed.
    raw = bridge.query("""SELECT json_build_object(
      'orders',(SELECT count(*) FROM purchase_orders),
      'receipts',(SELECT count(*) FROM purchase_receipts),
      'paid',(SELECT count(*) FROM purchase_orders WHERE payment_status='paid'),
      'digest',md5(jsonb_build_object(
        'orders',(SELECT jsonb_agg(to_jsonb(o) ORDER BY id) FROM purchase_orders o),
        'receipts',(SELECT jsonb_agg(to_jsonb(r) ORDER BY operation_id) FROM purchase_receipts r),
        'access',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM access_operations a))::text));""", db=db)
    return json.loads(raw)


def restore(manual=False):
    prefix = 'manual-' if manual else ''
    account = json.loads((STATE / (prefix + 'operator-account.json')).read_text())
    operator = local.session()
    _, _, actor = local.api(operator, '/api/v1/auth/login',
        {'email': account['email'], 'password': account['password']})
    _, _, catalogue = local.api(operator, '/api/v1/catalogue')
    plan_id = json.loads((STATE / (prefix + 'plan.json')).read_text())['plan_id']
    plan = next(p for p in catalogue['plans'] if p['plan_id'] == plan_id and any(
        price['currency'] == 'RUB' and price['amount_minor'] == '10000' for price in p['prices']))
    opener, login = signup(prefix + 'recovery')
    purchase = create_order(opener, login, plan, manual)
    target_account = login['account']['account_id']
    restored = local.restore_name('purchase')
    restore_url = None
    dump = None
    # Controlled outage affects only the native panel of this owned Docker project.
    compose('stop', 'panel')
    try:
        if manual:
            manual_report(opener, login, purchase)
            assert manual_decision(operator, actor, login, purchase)[0] == 202
        else:
            assert notification(purchase)[0] == 200
        paid = order(opener, purchase['order_id'])
        assert paid['payment_status'] == 'paid' and paid['fulfillment_status'] != 'applied'
        compose('stop', 'backend')
        before = financial_snapshot()
        dump = compose('exec', '-T', 'postgres', 'pg_dump', '-U', local.PG_USER, '-Fc', '-d', local.database())
        path = STATE / (prefix + 'pending-payment.dump')
        path.write_bytes(dump)
        path.chmod(0o600)
        bridge.query('CREATE DATABASE ' + restored + ';', db='postgres')  # Generated, validated identifier only.
        compose('exec', '-T', 'postgres', 'pg_restore', '-U', local.PG_USER,
                '--no-owner', '--no-privileges', '-d', restored, stdin=dump)
        source = urlsplit((local.STATE / 'database-url').read_text().strip())
        assert source.hostname == 'postgres' and source.username == local.PG_USER
        assert source.path == '/' + local.database()
        restore_url = STATE / ('restore-url-' + uuid4().hex)
        fd = os.open(restore_url, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, 'w') as output:
            output.write(urlunsplit(source._replace(path='/' + restored)) + '\n')
        compose('run', '--rm', '--no-deps', '-T', '-e', 'DATABASE_URL_FILE=/run/secrets/purchase_restore_url',
                '-v', str(restore_url) + ':/run/secrets/purchase_restore_url:ro', 'backend', 'migrate')
        assert financial_snapshot(restored) == before, 'restore changed paid order, receipt or saved access state'
        maintenance = (ROOT / 'backend/db/maintenance/post_restore_auth.sql').read_text()
        counts = bridge.query(maintenance, db=restored).splitlines()
        assert len(counts) == 3 and all(c.isdigit() for c in counts), 'restore auth maintenance failed'
        assert bridge.query(maintenance, db=restored) == '0\n0\n0', 'restore maintenance not idempotent'
        assert financial_snapshot(restored) == before, 'auth maintenance changed payment data'
        private(prefix + 'restore-report.json', {'paid_pending_preserved': True, 'financial_digest_matches': True,
                                      'auth_maintenance_idempotent': True, 'restored_writers_started': False})
    finally:
        if restore_url:
            restore_url.unlink(missing_ok=True)
        if dump:
            bridge.query('DROP DATABASE IF EXISTS ' + restored + ';', db='postgres')
        compose('up', '--no-build', '--pull', 'never', '--no-deps', '-d', 'panel')
        compose('up', '--no-build', '--pull', 'never', '--no-deps', '-d', 'backend')
    local.wait_until(local.ready)
    final = local.wait_until(lambda: applied(opener, purchase['order_id']), timeout=60)
    assert final['payment_status'] == 'paid' and panel(target_account)['exists']
    assert account_row(target_account)['access_operations'] == 1
    print('PASS: pending paid order restored read-only; source restart issued one real native access')


def main():
    actions = {'prepare': prepare, 'check': check, 'restore': restore,
               'prepare-manual': prepare_manual, 'check-manual': lambda: check(True),
               'restore-manual': lambda: restore(True)}
    if len(sys.argv) != 2 or sys.argv[1] not in actions:
        raise SystemExit('usage: local.py prepare|check|restore|prepare-manual|check-manual|restore-manual')
    actions[sys.argv[1]]()


if __name__ == '__main__':
    try:
        main()
    except Exception:
        # Accounts, cookies, form signatures and key replies must stay private.
        STATE.mkdir(mode=0o700, parents=True, exist_ok=True)
        import traceback
        private('failure.json', {'traceback': traceback.format_exc()})
        print('FAIL: owned purchase acceptance; private diagnostic saved', file=sys.stderr)
        raise SystemExit(1)
