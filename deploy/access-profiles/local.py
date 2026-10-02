"""Access profile owned-fixture/native bridge; restore writes only a fresh disposable DB."""
from hashlib import sha256
import importlib.util
import json
import os
from pathlib import Path
import sys
from uuid import UUID, uuid4

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('operations_local', ROOT / 'deploy/subscription-operations/local.py')
operations_bridge = importlib.util.module_from_spec(spec)
spec.loader.exec_module(operations_bridge)
local = operations_bridge.local
query = operations_bridge.query
PANEL = 'https://localhost:59444/'
TAGS = ('local-regular-vless', 'local-euru-vless',
        'local-unlimited-vless', 'local-probe-vless')
EVIDENCE = local.acceptance_state('access-profiles')


def uid(value):
    return str(UUID(value))


def digest(value):
    return sha256(json.dumps(value, separators=(',', ':'), sort_keys=True).encode()).hexdigest()


def own_patterns():
    return {'operations_pattern': local.fixture_pattern('subscription-operations'),
            'operations_new_pattern': 'subscription-operations-%@example.test',
            'profile_pattern': local.fixture_pattern('access-profiles'),
            'profile_new_pattern': 'access-profiles-%@example.test'}


def owned(account):
    account = uid(account)
    raw = query("""SELECT json_build_object(
      'owned',a.kind='web' AND ((a.email_key LIKE :'operations_pattern' OR a.email_key LIKE :'operations_new_pattern')
        OR (a.email_key LIKE :'profile_pattern' OR a.email_key LIKE :'profile_new_pattern')),
      'panel_key',a.panel_key,'vpn_id',a.vpn_id,'sub_id',a.sub_id,
      'assigned_panel_id',a.assigned_panel_id,'access_profile',a.access_profile,
      'latest_applied_profile',(SELECT x.target->>'profile' FROM access_operations x
        WHERE x.account_id=a.id AND x.status='applied'
        ORDER BY x.sequence DESC LIMIT 1),
      'vpn_banned',a.vpn_banned,
      'restricted',a.restricted,'had_subscription',a.had_subscription,
      'support_banned',EXISTS(SELECT 1 FROM support_conversations c
        WHERE c.account_id=a.id AND c.support_banned),
      'operator',EXISTS(SELECT 1 FROM operator_accounts r WHERE r.account_id=a.id),
      'unresolved_access',(SELECT count(*) FROM access_operations x WHERE x.account_id=a.id
        AND x.status IN ('pending','provisioning','needs_review')),
      'trial_grants',(SELECT count(*) FROM trial_grants g JOIN trial_operations t
        ON t.id=g.operation_id WHERE t.account_id=a.id),
      'access_operations',(SELECT count(*) FROM access_operations x WHERE x.account_id=a.id),
      'access_audit',(SELECT count(*) FROM audit_events e WHERE e.account_id=a.id
        AND e.access_operation_id IS NOT NULL))
      FROM accounts a WHERE a.id=:'account'::uuid;""", account=account, **own_patterns())
    if not raw:
        raise RuntimeError('Access profile owned account absent')
    row = json.loads(raw)
    if not row.pop('owned'):
        raise RuntimeError('Access profile owned account guard failed')
    return row


def registry(opener=None):
    opener = opener or local.login_panel()[0]
    inbounds = local.panel_call(opener, 'panel/api/inbounds/list')
    result = {}
    for inbound in inbounds:
        tag = inbound.get('tag')
        if tag not in TAGS:
            continue
        value = inbound.get('id')
        if tag in result or type(value) is not int or value <= 0 or inbound.get('enable') is not True:
            raise RuntimeError('Access profile owned inbound mapping changed')
        result[tag] = value
    if set(result) != set(TAGS) or len(set(result.values())) != len(TAGS):
        raise RuntimeError('Access profile owned inbound registry incomplete')
    return result


def panel(account):
    a = owned(account)
    result = {'exists': False, 'identity_matches': None,
              'identity_digest': digest([a['panel_key'], a['vpn_id'], a['sub_id'],
                                         a['assigned_panel_id']]),
              'assigned_server_digest': sha256(a['assigned_panel_id'].encode()).hexdigest()
                if a['assigned_panel_id'] else None}
    opener, _ = local.login_panel()
    status, _, raw = local.request(opener, PANEL + 'panel/api/clients/get/' + a['panel_key'])
    if status != 200:
        raise RuntimeError('Access profile native readback unavailable')
    reply = json.loads(raw)
    if reply.get('success') is False and reply.get('obj') is None:
        return result
    if reply.get('success') is not True or not isinstance(reply.get('obj'), dict):
        raise RuntimeError('Access profile native envelope changed')
    obj = reply['obj']
    client = obj['client']
    if client.get('email') != a['panel_key']:
        raise RuntimeError('Access profile native key mismatch')
    traffic = local.panel_call(opener, 'panel/api/clients/traffic/' + a['panel_key'])
    if traffic.get('uuid') != a['vpn_id'] or traffic.get('subId') != a['sub_id']:
        raise RuntimeError('Access profile native traffic identity mismatch')
    all_ids = sorted(obj['inboundIds'])
    known = registry(opener)
    if any(type(value) is not int or value not in known.values() for value in all_ids):
        raise RuntimeError('Access profile unknown native membership')
    managed = sorted(value for tag, value in known.items()
                     if tag != 'local-probe-vless' and value in all_ids)
    unmanaged = sorted(value for tag, value in known.items()
                       if tag == 'local-probe-vless' and value in all_ids)
    result.update({'exists': True,
      'identity_matches': client.get('uuid') == a['vpn_id'] and client.get('subId') == a['sub_id'],
      'expiry_ms': client['expiryTime'], 'limit_ip': client['limitIp'],
      'traffic_limit_bytes': client['totalGB'], 'enabled': client['enable'],
      'used_traffic': obj['usedTraffic'], 'up': traffic['up'], 'down': traffic['down'],
      'membership_digest': digest(all_ids),
      'managed_membership_digest': digest(managed),
      'unmanaged_membership_digest': digest(unmanaged),
      'managed_tags': sorted(tag for tag, value in known.items()
                             if tag != 'local-probe-vless' and value in managed),
      'unmanaged_present': bool(unmanaged)})
    if not result['identity_matches']:
        raise RuntimeError('Access profile native identity mismatch')
    return result


def snapshot(account):
    a = owned(account)
    return {'panel': panel(account), 'vpn_banned': a['vpn_banned'],
            'access_profile': a['access_profile'], 'operator': a['operator'],
            'latest_applied_profile': a['latest_applied_profile'],
            'unresolved_access': a['unresolved_access'],
            'support_banned': a['support_banned'],
            'restricted': a['restricted'], 'had_subscription': a['had_subscription'],
            'trial_grants': a['trial_grants'], 'access_operations': a['access_operations'],
            'access_audit': a['access_audit']}


def state(account):
    a = owned(account)
    return {'access_profile': a['access_profile'],
            'latest_applied_profile': a['latest_applied_profile'],
            'vpn_banned': a['vpn_banned'],
            'restricted': a['restricted'], 'had_subscription': a['had_subscription'],
            'support_banned': a['support_banned'],
            'operator': a['operator'], 'unresolved_access': a['unresolved_access'],
            'trial_grants': a['trial_grants'], 'access_operations': a['access_operations'],
            'access_audit': a['access_audit'],
            'assigned': a['assigned_panel_id'] is not None}


def operation(value):
    value = uid(value)
    raw = query("""SELECT json_build_object('kind',o.kind,'status',o.status,
      'steps',o.completed_steps,'system_origin',o.operator_account_id IS NULL,
      'monthly_period',o.monthly_period,
      'jobs',(SELECT count(*) FROM river_job j WHERE j.kind='access_operation'
        AND j.args->>'operation_id'=o.id::text),
      'requested_audit',(SELECT count(*) FROM audit_events e
        WHERE e.access_operation_id=o.id AND e.action='access_requested'),
      'applied_audit',(SELECT count(*) FROM audit_events e
        WHERE e.access_operation_id=o.id AND e.action='access_applied'))
      FROM access_operations o JOIN accounts a ON a.id=o.account_id
      WHERE o.id=:'operation'::uuid AND a.kind='web'
        AND ((a.email_key LIKE :'operations_pattern' OR a.email_key LIKE :'operations_new_pattern')
             OR (a.email_key LIKE :'profile_pattern' OR a.email_key LIKE :'profile_new_pattern'));""",
        operation=value, **own_patterns())
    if not raw:
        raise RuntimeError('Access profile owned operation absent')
    return json.loads(raw)


def restore_state(db=None):
    raw = query("""SELECT json_build_object(
      'owned_accounts',(SELECT count(*) FROM accounts a WHERE a.kind='web'
        AND ((a.email_key LIKE :'operations_pattern' OR a.email_key LIKE :'operations_new_pattern')
          OR (a.email_key LIKE :'profile_pattern' OR a.email_key LIKE :'profile_new_pattern'))),
      'account_profile_ban_digest',coalesce((SELECT md5(string_agg(
        md5(json_build_array(a.id,a.access_profile,a.vpn_banned)::text),',' ORDER BY a.id))
        FROM accounts a WHERE a.kind='web' AND ((a.email_key LIKE :'operations_pattern' OR a.email_key LIKE :'operations_new_pattern')
          OR (a.email_key LIKE :'profile_pattern' OR a.email_key LIKE :'profile_new_pattern'))),'empty'),
      'operations',(SELECT count(*) FROM access_operations),
      'operation_digest',coalesce((SELECT md5(string_agg(md5(to_jsonb(o)::text),',' ORDER BY o.id))
        FROM access_operations o),'empty'),
      'access_audit',(SELECT count(*) FROM audit_events WHERE access_operation_id IS NOT NULL),
      'audit_digest',coalesce((SELECT md5(string_agg(md5(to_jsonb(e)::text),',' ORDER BY e.id))
        FROM audit_events e WHERE e.access_operation_id IS NOT NULL),'empty'),
      'monthly_periods',(SELECT count(*) FROM monthly_reset_periods),
      'monthly_period_digest',coalesce((SELECT md5(string_agg(md5(to_jsonb(p)::text),','
         ORDER BY p.account_id,p.local_period)) FROM monthly_reset_periods p),'empty'));
    """, db=db, **own_patterns())
    return json.loads(raw)


def restore():
    manifest_path = os.environ.get('ACCEPTANCE_RUNTIME_MANIFEST')
    if not manifest_path:
        raise RuntimeError('root Access profile runtime manifest required')
    manifest = json.loads(Path(manifest_path).read_text())
    images = manifest.get('images', {})
    transport = operations_bridge.restrictions_bridge.transport()
    if (not manifest.get('health') or not manifest.get('vpn_unchanged')
        or manifest.get('fault_mode') != 'off' or not manifest.get('bot_stopped')
        or len(manifest.get('revision', '')) != 40
        or any(not images.get(name, '').startswith('sha256:')
               for name in ('backend', 'gateway', 'panel'))
        or transport['project'] != manifest.get('project')
        or not transport['bot_stopped'] or not transport['reconcile_stopped']
        or not transport['no_telegram_operators'] or not transport['vpn_connected']
        or transport['vpn_config_digest'] != manifest.get('vpn_config_sha256')):
        raise RuntimeError('root Access profile runtime manifest not ready')
    EVIDENCE.mkdir(parents=True, exist_ok=True, mode=0o700)
    EVIDENCE.chmod(0o700)
    path = EVIDENCE / 'restore.jsonl'
    fd = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    command = 'ACCEPTANCE_RUNTIME_MANIFEST=' + manifest_path + " python3 deploy/access-profiles/local.py restore"

    def evidence(name, expected, actual, ok):
        os.write(fd, (json.dumps({'criterion': name,
          'target': f'{local.PROJECT} own PostgreSQL and fresh disposable restore DB',
          'command': command, 'expected': expected, 'actual': actual,
          'verdict': 'PASS' if ok else 'FAIL', 'artifacts': [str(path)]}) + '\n').encode())
        if not ok:
            raise RuntimeError('Access profile disposable restore assertion failed')

    phase = 'source snapshot'
    try:
        before = restore_state()
        if before['owned_accounts'] < 1 or before['operations'] < 1:
            raise RuntimeError('Access profile applied owned fixtures required')
        phase = 'fresh disposable database and pipe restore'
        name = local.restore_name('access-profiles')
        query('CREATE DATABASE ' + name + ';', db='postgres')
        dump = local.compose('exec', '-T', 'postgres', 'pg_dump', '-U', local.PG_USER,
                             '-Fc', '-d', local.database())
        local.compose('exec', '-T', 'postgres', 'pg_restore', '-U', local.PG_USER,
                      '--no-owner', '--no-privileges', '-d', name, stdin=dump)
        restored = restore_state(name)
        evidence('AC8 Access profile disposable backup/restore',
          'owned profile/ban, access operations/audit and monthly period rows/digests equal after pipe restore',
          {'equal': before == restored, 'owned_accounts': restored['owned_accounts'],
           'operations': restored['operations'], 'access_audit': restored['access_audit'],
           'monthly_periods': restored['monthly_periods']}, before == restored)
        phase = 'isolated monthly uniqueness'
        source = query("""SELECT id FROM accounts WHERE kind='web' AND
          (email_key LIKE :'profile_pattern' OR email_key LIKE :'profile_new_pattern')
          ORDER BY created_at LIMIT 1;""", db=name, **own_patterns())
        if not source:
            raise RuntimeError('Access profile own restored account absent')
        account = uid(source)
        period = '2099-01'
        insert = """INSERT INTO monthly_reset_periods(account_id,local_period,timezone,status,created_at,updated_at)
          VALUES(:'account'::uuid,'2099-01','UTC','waiting',now(),now()) ON CONFLICT DO NOTHING
          RETURNING local_period;"""
        first = query(insert, db=name, account=account)
        second = query(insert, db=name, account=account)
        cardinality = query("""SELECT count(*) FROM monthly_reset_periods
          WHERE account_id=:'account'::uuid AND local_period='2099-01';""",
          db=name, account=account)
        evidence('AC6/8 disposable monthly uniqueness',
          'same owned account/local period inserts once even on duplicate attempt; live scheduler untouched',
          {'first_insert': first == period, 'duplicate_no_insert': second == '',
           'unique_row': cardinality == '1'},
          first == period and second == '' and cardinality == '1')
        with_period = restore_state(name)
        phase = 'restored authentication cleanup'
        cleanup = (ROOT / 'backend/db/maintenance/post_restore_auth.sql').read_text()
        query(cleanup, db=name)
        second_cleanup = query(cleanup, db=name)
        residual = query("""SELECT count(*) FROM sessions;
          SELECT count(*) FROM credential_challenges WHERE NOT revoked AND used_at IS NULL;
          SELECT count(*) FROM mail_deliveries WHERE kind='credential' AND ciphertext IS NOT NULL;""", db=name)
        after = restore_state(name)
        evidence('AC8 Access profile repeated restored authentication cleanup',
          'second cleanup zeros sessions/live proofs/credential ciphertext while preserving profiles, ban, operations, audit and periods',
          {'second_zero': second_cleanup == '0\n0\n0', 'residual_zero': residual == '0\n0\n0',
           'product_state_equal': after == with_period},
          second_cleanup == '0\n0\n0' and residual == '0\n0\n0' and after == with_period)
        phase = 'second disposable pipe restore with synthetic period'
        second_name = local.restore_name('access-profiles')
        query('CREATE DATABASE ' + second_name + ';', db='postgres')
        second_dump = local.compose('exec', '-T', 'postgres', 'pg_dump', '-U',
                                    local.PG_USER, '-Fc', '-d', name)
        local.compose('exec', '-T', 'postgres', 'pg_restore', '-U', local.PG_USER,
                      '--no-owner', '--no-privileges', '-d', second_name,
                      stdin=second_dump)
        second_restored = restore_state(second_name)
        duplicate = query(insert, db=second_name, account=account)
        retained = query("""SELECT count(*) FROM monthly_reset_periods
          WHERE account_id=:'account'::uuid AND local_period='2099-01';""",
          db=second_name, account=account)
        evidence('AC6/8 synthetic period survives second disposable restore',
          'period row and all profile/ban/operation/audit digests survive pipe backup to a second fresh DB; duplicate account/month still rejected',
          {'state_equal': second_restored == after,
           'periods': second_restored['monthly_periods'],
           'duplicate_no_insert': duplicate == '', 'unique_row': retained == '1'},
          second_restored == after and second_restored['monthly_periods'] >= 1
          and duplicate == '' and retained == '1')
        return {'checks': 4, 'passed': 4, 'evidence': str(path)}
    except Exception as error:
        os.write(fd, (json.dumps({'criterion': 'Access profile restore stopped',
          'target': f'{local.PROJECT} fresh disposable restore DB',
          'command': command, 'expected': 'approved source and isolated database preconditions',
          'actual': {'phase': phase, 'error_class': type(error).__name__},
          'verdict': 'BLOCKED', 'artifacts': [str(path)]}) + '\n').encode())
        raise
    finally:
        os.close(fd)


def run(data):
    action = data['action']
    if action == 'transport':
        return operations_bridge.restrictions_bridge.transport()
    if action == 'registry':
        return registry()
    if action == 'snapshot':
        return snapshot(data['account'])
    if action == 'state':
        return state(data['account'])
    if action == 'operation':
        return operation(data['operation'])
    if action == 'restore':
        return restore()
    raise ValueError('unknown Access profile bridge action')


if __name__ == '__main__':
    try:
        if len(sys.argv) > 1:
            if sys.argv[1:] != ['restore']:
                raise ValueError('unknown Access profile CLI command')
            payload = {'action': 'restore'}
        else:
            payload = json.load(sys.stdin)
        print(json.dumps(run(payload), separators=(',', ':')))
    except Exception:
        print(json.dumps({'error': 'Access profile local bridge failed'}))
        sys.exit(1)
