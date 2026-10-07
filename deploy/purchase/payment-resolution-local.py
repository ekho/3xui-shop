"""Owned C20 native refund/restart check; synthetic incoming money only."""
import json
import os
import sys
from urllib.parse import quote
from urllib.error import HTTPError
from uuid import uuid4

import local as purchase

local, bridge = purchase.local, purchase.bridge
assert local.PROFILE == 'native' and local.PROJECT == 'cabinet-c20', 'requires own cabinet-c20 project'
purchase.STATE = local.STATE / 'purchase'
bridge.STATE = local.STATE / 'roles'


def prepare():
    local.prepare()
    config = dict(line.split('=', 1) for line in local.ENV.read_text().splitlines()
                  if line and not line.startswith('#'))
    config.update(APP_NETWORK_SUBNET='10.253.20.0/28', APP_GATEWAY_IP='10.253.20.14')
    local.write('public.env', '\n'.join(k + '=' + v for k, v in config.items()) + '\n')
    purchase.prepare()


def process():
    container = purchase.compose('ps', '-q', 'backend').decode().strip()
    return json.loads(local.command(['docker', 'inspect', '--format',
        '{"image":"{{.Image}}","started":"{{.State.StartedAt}}"}', container]))


def panel(account):
    result = purchase.panel(account)
    row = purchase.account_row(account)
    opener, _ = local.login_panel()
    traffic = local.panel_call(opener, 'panel/api/clients/traffic/' + quote(row['panel_key'], safe=''))
    assert traffic['uuid'] == row['vpn_id'] and traffic['subId'] == row['sub_id']
    result.update(up=traffic['up'], down=traffic['down'])
    return result


def seed_traffic(account):
    row = purchase.account_row(account)
    purchase.compose('stop', 'panel')
    try:
        # Same owned Docker-VM SQLite fixture pattern as exhaust_counters.
        script = """import json,sqlite3,sys
row=json.load(sys.stdin)
with sqlite3.connect('/panel/x-ui.db') as db:
    assert db.execute('SELECT count(*) FROM clients WHERE email=? AND uuid=? AND sub_id=?',
        (row['panel_key'],row['vpn_id'],row['sub_id'])).fetchone()[0]==1
    assert db.execute('UPDATE client_traffics SET up=111,down=222 WHERE email=?',
        (row['panel_key'],)).rowcount>0
"""
        image = (purchase.ROOT / 'deploy/acceptance/Dockerfile.bot').read_text().splitlines()[0].split()[1]
        local.command(['docker', 'run', '--rm', '--pull', 'missing', '--network', 'none',
            '--read-only', '--user', str(os.getuid()) + ':' + str(os.getgid()), '-i',
            '--mount', 'type=bind,source=' + str(local.STATE / 'panel-db') + ',target=/panel',
            '--entrypoint', 'python', image, '-c', script], stdin=json.dumps(row).encode())
    finally:
        purchase.compose('up', '--no-build', '--pull', 'never', '--no-deps', '-d', 'panel')
    def ready():
        try:
            return panel(account)
        except HTTPError as error:
            if error.code in (502, 503):
                return None
            raise
        except OSError:
            return None
    local.wait_until(ready, timeout=30)
    assert panel(account)['up'] > 0 and panel(account)['down'] > 0


def saved(order):
    return json.loads(bridge.query("""SELECT json_build_object(
      'money',md5((to_jsonb(p)-ARRAY['active','fulfillment_status','review_required','review_reason','updated_at'])::text),
      'receipts',coalesce((SELECT md5(jsonb_agg(to_jsonb(r) ORDER BY operation_id)::text)
        FROM purchase_receipts r WHERE r.order_id=p.id),'none'),
      'receipt_count',(SELECT count(*) FROM purchase_receipts WHERE order_id=p.id),
      'refund_count',(SELECT count(*) FROM purchase_refunds WHERE order_id=p.id),
      'access',p.access_operation_id,'status',o.status,'target',o.target,'desired',o.desired,
      'steps',o.completed_steps,'write_started',o.write_started,
      'reset_started',o.reset_started,'reset_acknowledged',o.reset_acknowledged,
      'access_count',(SELECT count(*) FROM access_operations WHERE purchase_order_id=p.id),
      'job',(SELECT state FROM river_job WHERE kind='access_operation'
        AND args->>'operation_id'=p.access_operation_id::text ORDER BY id DESC LIMIT 1))
      FROM purchase_orders p LEFT JOIN access_operations o ON o.id=p.access_operation_id
      WHERE p.id=:'order'::uuid;""", order=order['order_id']))


def refund(operator, actor, opener, login, order, receipt):
    path = '/api/v1/operator/clients/' + purchase.owned(login['account']['account_id']) + '/orders/' + order['order_id']
    status, _, case = local.api(operator, path + '/payment-case', {'receipt_operation_id': receipt}, actor['csrf_token'])
    assert status == 200 and case['can_confirm_refund'], 'selected receipt not refundable'
    body = {'receipt_operation_id': receipt, 'reference': 'local-return-' + uuid4().hex,
            'reason': 'Owned full external-return attestation; no real transfer',
            'confirm_full': True, 'keep_access': True}
    key = str(uuid4())
    first = local.api(operator, path + '/refunds', body, actor['csrf_token'], key)
    assert first[0] == 201 and first[2]['source'] == 'operator'
    assert first[2]['returned_amount'] == '100.00' and first[2]['returned_currency'] == 'RUB'
    assert local.api(operator, path + '/refunds', body, actor['csrf_token'], key)[2] == first[2]
    assert purchase.notification(order, receipt=receipt)[0] == 200
    history = local.api(opener, '/api/v1/payment-history',
                        {'kind': 'refunds'}, login['csrf_token'])[2]
    assert any(row == first[2] for row in history['refunds'])
    return first[2]


def check():
    config = json.loads(purchase.compose('config', '--format', 'json'))
    env = config['services']['backend']['environment']
    assert env['TELEGRAM_ENABLED'] == 'false' and env['LEGACY_BOT_API_ENABLED'] == 'false'
    assert env['SHOP_PAYMENT_YOOMONEY_ENABLED'] == 'true' and env['YOOMONEY_WALLET_ID'] == '410000000000000'
    assert config['services']['panel']['image'].startswith('ghcr.io/mhsanaei/3x-ui:3.7.0@sha256:')
    assert not set(purchase.compose('ps', '--services', '--status', 'running').decode().splitlines()).intersection({'bot', 'reconcile'})
    operator, actor = purchase.signup('operator')
    assert bridge.role('grant', actor['account']['account_id'])
    devices = int(bridge.query("""SELECT min(n) FROM generate_series(3,10000) n
      WHERE NOT EXISTS(SELECT 1 FROM catalogue_plans WHERE NOT archived AND current_devices=n);"""))
    terms = {'devices': devices, 'traffic_gb': 30, 'profile': 'regular', 'hidden': False, 'periods': [30],
             'prices': [{'period_days': 30, 'currency': c, 'amount_minor': a}
                        for c, a in [('RUB', '10000'), ('USD', '200'), ('XTR', '300')]]}
    status, _, plan = local.api(operator, '/api/v1/operator/catalogue/plans',
        {'terms': terms, 'reason': 'Owned refund acceptance'}, actor['csrf_token'], str(uuid4()))
    assert status == 201
    results = []
    for name in ('prepared', 'applied'):
        opener, login = purchase.signup(name)
        account = login['account']['account_id']
        if name == 'prepared':
            _, _, trial = local.api(opener, '/api/v1/trial-requests',
                {'comment': 'Owned refund preserves trial'}, login['csrf_token'], str(uuid4()))
            local.api(operator, '/api/v1/operator/trial-requests/' + trial['request_id'] + '/decision',
                {'decision': 'approve', 'reason': ''}, actor['csrf_token'], str(uuid4()))
            local.wait_until(lambda: local.active(opener))
            seed_traffic(account)
        order = purchase.create_order(opener, login, plan)
        if name == 'prepared':
            bridge.query("""CREATE FUNCTION refund_hold_access() RETURNS trigger LANGUAGE plpgsql AS $$
              BEGIN IF NEW.kind='access_operation' AND EXISTS(SELECT 1 FROM access_operations
                WHERE id=(NEW.args->>'operation_id')::uuid AND purchase_order_id=TG_ARGV[0]::uuid)
                THEN NEW.state='scheduled';NEW.scheduled_at=clock_timestamp()+interval '1 hour';END IF;RETURN NEW;END $$;
              CREATE TRIGGER refund_hold_access BEFORE INSERT ON river_job FOR EACH ROW EXECUTE FUNCTION refund_hold_access(:'order');""",
                order=order['order_id'])
        try:
            assert (notice := purchase.notification(order))[0] == 200
            if name == 'prepared':
                before = local.wait_until(lambda: s if (s := saved(order))['target'] and s['job'] == 'scheduled' else None)
            else:
                local.wait_until(lambda: purchase.applied(opener, order['order_id']), timeout=60)
                seed_traffic(account)
                before = saved(order)
            native = panel(account)
            record = refund(operator, actor, opener, login, order, notice[1])
            after = saved(order)
            assert after['status'] == ('skipped' if name == 'prepared' else 'applied')
            for field in ('money','receipts','receipt_count','access','target','desired','steps','write_started','reset_started','reset_acknowledged','access_count'):
                assert after[field] == before[field], 'refund changed ' + field
            assert after['refund_count'] == 1 and panel(account) == native
            processes = [process()]
            if name == 'prepared':
                bridge.query('DROP TRIGGER refund_hold_access ON river_job;DROP FUNCTION refund_hold_access();')
            for _ in range(2):
                purchase.compose('stop', 'backend')
                bridge.query("""UPDATE river_job SET state='available',finalized_at=NULL,scheduled_at=clock_timestamp()
                  WHERE kind='access_operation' AND args->>'operation_id'=:'operation';""", operation=after['access'])
                purchase.compose('up', '--no-build', '--pull', 'never', '-d', 'backend')
                local.wait_until(local.ready)
                processes.append(process())
                assert processes[-1]['image'] == processes[0]['image'] and processes[-1]['started'] != processes[-2]['started']
                local.wait_until(lambda: saved(order)['job'] == 'completed')
                assert {k:v for k,v in saved(order).items() if k != 'job'} == {k:v for k,v in after.items() if k != 'job'}
                assert panel(account) == native, 'restart changed current native access/traffic'
            purchase.private(name + '-refund-proof.json', {'before': before, 'after': saved(order),
                'panel': native, 'processes': processes, 'refund': record})
            results.append({'fixture': name, 'two_restarts': True, 'saved_target_preserved': True,
                            'nonzero_traffic_preserved': True, 'one_refund': True})
        finally:
            bridge.query('DROP TRIGGER IF EXISTS refund_hold_access ON river_job;DROP FUNCTION IF EXISTS refund_hold_access();')
            purchase.compose('up', '--no-build', '--pull', 'never', '-d', 'backend')
    purchase.private('refund-native-report.json', {'panel_version': '3.7.0', 'results': results,
        'real_payment': False, 'live_vpn_changed': False})
    print('PASS: prepared funding retired, applied access retained; frozen target/money/nonzero traffic survive two real Go restarts each', flush=True)


if __name__ == '__main__':
    assert len(sys.argv) == 2 and sys.argv[1] in ('prepare', 'up', 'check', 'down'), 'prepare|up|check|down required'
    if sys.argv[1] == 'prepare':
        prepare()
    elif sys.argv[1] == 'up':
        local.up()
        purchase.compose('up', '--no-build', '--pull', 'never', '-d', 'backend')
        local.wait_until(local.ready)
    elif sys.argv[1] == 'check':
        check()
    else:
        purchase.compose('down', '--volumes', '--remove-orphans')
