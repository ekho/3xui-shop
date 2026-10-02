"""Subscription operation owned-fixture bridge. JSON stdin, redacted JSON stdout; never prints panel secrets."""
import base64
from hashlib import sha256
import importlib.util
import json
import os
from pathlib import Path
import re
import sys
import time
from urllib.parse import parse_qs, urlsplit
import urllib.request
from uuid import UUID, uuid4

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('account_restrictions_local', ROOT / 'deploy/account-restrictions/local.py')
restrictions_bridge = importlib.util.module_from_spec(spec)
spec.loader.exec_module(restrictions_bridge)
local = restrictions_bridge.local
STATE = local.acceptance_state('subscription-operations')


def uid(value):
    return str(UUID(value))


def write_private(name, value):
    STATE.mkdir(parents=True, exist_ok=True, mode=0o700)
    STATE.chmod(0o700)
    path = STATE / name
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as stream:
        stream.write(json.dumps(value, separators=(',', ':')) + '\n')
    return path


def query(sql, db=None, **ids):
    return restrictions_bridge.query(sql, db=db, **ids)


def owned(account, operator=False):
    account = uid(account)
    raw = query("""SELECT json_build_object('owned',a.kind='web'
        AND (a.email_key LIKE :'fixture_pattern' OR a.email_key LIKE :'new_fixture_pattern'),
        'operator',EXISTS(SELECT 1 FROM operator_accounts o WHERE o.account_id=a.id))
        FROM accounts a WHERE a.id=:'account'::uuid;""", account=account,
        **restrictions_bridge.fixture_patterns('subscription-operations'))
    if not raw:
        raise RuntimeError('Subscription operation account absent')
    row = json.loads(raw)
    if not row['owned'] or operator and not row['operator']:
        raise RuntimeError('Subscription operation fixture ownership guard failed')
    return account


def account_row(account):
    account = owned(account)
    raw = query("""SELECT json_build_object(
      'account_id',a.id,'panel_key',a.panel_key,'vpn_id',a.vpn_id,
      'sub_id',a.sub_id,'assigned_panel_id',a.assigned_panel_id,
      'vpn_banned',a.vpn_banned,'restricted',a.restricted,
      'had_subscription',a.had_subscription,
      'trial_operations',(SELECT count(*) FROM trial_operations t WHERE t.account_id=a.id),
      'trial_grants',(SELECT count(*) FROM trial_grants g JOIN trial_operations t
        ON t.id=g.operation_id WHERE t.account_id=a.id),
      'access_operations',(SELECT count(*) FROM access_operations x WHERE x.account_id=a.id),
      'access_audit',(SELECT count(*) FROM audit_events e WHERE e.account_id=a.id
        AND e.access_operation_id IS NOT NULL))
      FROM accounts a WHERE a.id=:'account'::uuid;""", account=account)
    return json.loads(raw)


def panel(account):
    a = account_row(account)
    base = {'exists': False, 'identity_matches': None,
      'allocated_identity_digest': sha256(json.dumps(
        [a['panel_key'], a['vpn_id'], a['sub_id']],
        separators=(',', ':')).encode()).hexdigest(),
      'assigned_server_digest': sha256(a['assigned_panel_id'].encode()).hexdigest()
        if a['assigned_panel_id'] else None,
      'identity_digest': sha256(
        json.dumps([a['panel_key'], a['vpn_id'], a['sub_id'], a['assigned_panel_id']],
                   separators=(',', ':')).encode()).hexdigest()}
    opener, _ = local.login_panel()
    # The native 3X-UI 3.7.0 absence envelope is known from Web trial preflight.
    status, _, raw = local.request(opener, 'https://localhost:59444/panel/api/clients/get/' + a['panel_key'])
    reply = json.loads(raw)
    if status != 200:
        raise RuntimeError('owned panel readback unavailable')
    if reply.get('success') is False and reply.get('obj') is None:
        return base
    if reply.get('success') is not True or not isinstance(reply.get('obj'), dict):
        raise RuntimeError('owned panel readback shape changed')
    obj = reply['obj']
    client = obj['client']
    if client.get('email') != a['panel_key']:
        raise RuntimeError('panel fixture key mismatch')
    traffic = local.panel_call(opener, 'panel/api/clients/traffic/' + a['panel_key'])
    if traffic.get('uuid') != a['vpn_id'] or traffic.get('subId') != a['sub_id']:
        raise RuntimeError('panel traffic identity mismatch')
    base.update({'exists': True,
      'identity_matches': client.get('uuid') == a['vpn_id'] and client.get('subId') == a['sub_id'],
      'expiry_ms': client['expiryTime'], 'limit_ip': client['limitIp'],
      'traffic_limit_bytes': client['totalGB'], 'enabled': client['enable'],
      'used_traffic': obj.get('usedTraffic'), 'up': traffic['up'], 'down': traffic['down'],
      'membership_digest': sha256(json.dumps(sorted(obj['inboundIds'])).encode()).hexdigest()})
    if not base['identity_matches']:
        raise RuntimeError('panel identity mismatch')
    return base


def snapshot(account):
    a = account_row(account)
    return {'panel': panel(account), 'vpn_banned': a['vpn_banned'],
            'restricted': a['restricted'], 'had_subscription': a['had_subscription'],
            'trial_operations': a['trial_operations'], 'trial_grants': a['trial_grants'],
            'access_operations': a['access_operations'], 'access_audit': a['access_audit']}


def operation(op):
    op = uid(op)
    raw = query("""SELECT json_build_object('account_id',o.account_id,'kind',o.kind,
      'status',o.status,'desired',o.desired,'steps',o.completed_steps,
      'review_reason',o.review_reason,'write_started',o.write_started,
      'reset_started',o.reset_started,'attempts',o.attempts,
      'target',o.target,'target_digest',md5(o.target::text),
      'requested_audit',(SELECT count(*) FROM audit_events e WHERE e.access_operation_id=o.id
        AND e.action='access_requested'),
      'applied_audit',(SELECT count(*) FROM audit_events e WHERE e.access_operation_id=o.id
        AND e.action='access_applied'),
      'jobs',(SELECT count(*) FROM river_job j WHERE j.kind='access_operation'
        AND j.args->>'operation_id'=o.id::text))
      FROM access_operations o WHERE o.id=:'op'::uuid;""", op=op)
    if not raw:
        raise RuntimeError('Subscription operation operation absent')
    row = json.loads(raw)
    account = owned(row.pop('account_id'))
    target = row.pop('target')
    native = panel(account)
    if native['exists']:
        limit = target['device_count'] + 1 if target['device_count'] else 0
        row['panel_matches_target'] = (native['identity_matches'] and
          native['expiry_ms'] == target['expiry_time_ms'] and
          native['limit_ip'] == limit and
          native['traffic_limit_bytes'] == target['traffic_limit_bytes'] and
          native['membership_digest'] == sha256(json.dumps(
              sorted(target['inbound_ids'])).encode()).hexdigest() and
          (not target['banned'] or native['enabled'] is False))
    else:
        row['panel_matches_target'] = False
    return row


def native_update_payload(account_row_value, client):
    if uid(client['uuid']) != account_row_value['vpn_id'] or client['subId'] != account_row_value['sub_id']:
        raise RuntimeError('Subscription operation fixture update identity mismatch')
    # 3X-UI 3.7.0 readback is ClientRecord; update binds model.Client.
    payload = {**client, 'id': client['uuid'], 'created_at': client['createdAt']}
    payload['allowedIPs'] = [ip.strip() for ip in client['allowedIPs'].split(',') if ip.strip()]
    if isinstance(payload.get('reverse'), str):
        payload['reverse'] = json.loads(payload['reverse']) if payload['reverse'].strip() else None
    return payload


def fixture(account, kind):
    a = account_row(account)
    if a['access_operations'] or a['trial_grants'] != 1 or not a['assigned_panel_id']:
        raise RuntimeError('Subscription operation fixture requires one applied trial and no access writes')
    before = panel(account)
    if not before['exists'] or not before['identity_matches']:
        raise RuntimeError('Subscription operation fixture native client missing')
    opener, csrf = local.login_panel()
    raw = local.panel_call(opener, 'panel/api/clients/get/' + a['panel_key'])
    client = raw['client']
    payload = native_update_payload(a, client)
    if kind in ('expire', 'perpetual'):
        desired = int(time.time() * 1000) - 24 * 3600 * 1000 if kind == 'expire' else 0
        if kind == 'expire' and before['expiry_ms'] <= int(time.time() * 1000):
            raise RuntimeError('Subscription operation expire fixture already expired')
        if kind == 'perpetual' and before['expiry_ms'] == 0:
            raise RuntimeError('Subscription operation perpetual fixture already perpetual')
        local.panel_call(opener, 'panel/api/clients/update/' + a['panel_key'],
                         {**payload, 'expiryTime': desired}, csrf)
        after = panel(account)
        unchanged = ('identity_digest', 'limit_ip', 'traffic_limit_bytes', 'membership_digest',
                     'used_traffic', 'up', 'down')
        if after['expiry_ms'] != desired or any(after[k] != before[k] for k in unchanged):
            raise RuntimeError('Subscription operation fixture update changed unrelated fields')
    elif kind == 'ban':
        query("""UPDATE accounts SET vpn_banned=true WHERE id=:'account'::uuid
          AND kind='web' AND (email_key LIKE :'fixture_pattern' OR email_key LIKE :'new_fixture_pattern');""",
          account=uid(account), **restrictions_bridge.fixture_patterns('subscription-operations'))
        local.panel_call(opener, 'panel/api/clients/bulkDisable',
                         {'emails': [a['panel_key']]}, csrf)
        after = panel(account)
        if after['enabled'] or not account_row(account)['vpn_banned']:
            raise RuntimeError('Subscription operation VPN-ban fixture not confirmed')
    else:
        raise ValueError('unknown Subscription operation fixture kind')
    return {'fixture': kind, 'before': before, 'after': after}


def recovery_expire(account):
    a = account_row(account)
    if a['trial_grants'] != 1 or a['access_operations'] != 1 or a['vpn_banned'] or not a['assigned_panel_id']:
        raise RuntimeError('Subscription operation recovery fixture account state changed')
    history = json.loads(query("""SELECT json_build_object(
      'total',count(*),
      'applied_comp',count(*) FILTER (WHERE kind='compensate' AND status='applied'),
      'unresolved',count(*) FILTER (WHERE status<>'applied'))
      FROM access_operations WHERE account_id=:'account'::uuid;""", account=uid(account)))
    if history != {'total': 1, 'applied_comp': 1, 'unresolved': 0}:
        raise RuntimeError('Subscription operation recovery fixture requires sole applied compensation')
    before = panel(account)
    if (not before['exists'] or not before['identity_matches'] or before['enabled'] is not False or
        before['expiry_ms'] <= int(time.time() * 1000) or before['traffic_limit_bytes'] <= 0 or
        before['used_traffic'] >= before['traffic_limit_bytes']):
        raise RuntimeError('Subscription operation future disabled nonexhausted fixture absent')
    opener, csrf = local.login_panel()
    raw = local.panel_call(opener, 'panel/api/clients/get/' + a['panel_key'])
    payload = native_update_payload(a, raw['client'])
    desired = int(time.time() * 1000) - 24 * 3600 * 1000
    local.panel_call(opener, 'panel/api/clients/update/' + a['panel_key'],
                     {**payload, 'expiryTime': desired}, csrf)
    after = panel(account)
    unchanged = ('identity_digest', 'limit_ip', 'traffic_limit_bytes',
                 'membership_digest', 'used_traffic', 'up', 'down')
    if (after['expiry_ms'] != desired or after['enabled'] is not False or
        any(after[k] != before[k] for k in unchanged) or
        account_row(account)['access_operations'] != 1):
        raise RuntimeError('Subscription operation recovery fixture changed unrelated state')
    return {'before': before, 'after': after}


def fixture_unlimited(account, reference_account):
    a = account_row(account)
    if a['trial_operations'] or a['trial_grants'] or a['access_operations'] or a['had_subscription'] or a['assigned_panel_id']:
        raise RuntimeError('Subscription operation unlimited fixture requires fresh unassigned account')
    if panel(account)['exists']:
        raise RuntimeError('Subscription operation unlimited fixture already has client')
    reference = account_row(reference_account)
    if not reference['assigned_panel_id'] or not panel(reference_account)['exists']:
        raise RuntimeError('Subscription operation reference client must be assigned on owned panel')
    panel_id = reference['assigned_panel_id']
    if not re.fullmatch(r'[A-Za-z0-9_-]{1,80}', panel_id):
        raise RuntimeError('Subscription operation configured panel ID invalid')
    opener, csrf = local.login_panel()
    rows = local.panel_call(opener, 'panel/api/inbounds/list')
    matched = {row.get('tag'): row.get('id') for row in rows
               if row.get('tag') in ('local-regular-vless', 'local-unlimited-vless')
               and row.get('enable') is True}
    if set(matched) != {'local-regular-vless', 'local-unlimited-vless'}:
        raise RuntimeError('root-owned unlimited fixture inbounds absent')
    client = {'email': a['panel_key'], 'id': a['vpn_id'], 'subId': a['sub_id'],
      'expiryTime': int(time.time() * 1000) + 7 * 86400000,
      'enable': True, 'totalGB': 0, 'limitIp': 2, 'flow': 'xtls-rprx-vision'}
    local.panel_call(opener, 'panel/api/clients/add',
                     {'client': client, 'inboundIds': sorted(matched.values())}, csrf)
    try:
        updated = query("""UPDATE accounts SET assigned_panel_id='""" + panel_id + """'
          WHERE id=:'account'::uuid AND kind='web'
            AND (email_key LIKE :'fixture_pattern' OR email_key LIKE :'new_fixture_pattern')
            AND assigned_panel_id IS NULL
          RETURNING assigned_panel_id;""", account=uid(account),
          **restrictions_bridge.fixture_patterns('subscription-operations'))
        if updated != panel_id:
            raise RuntimeError('Subscription operation unlimited fixture account assignment failed')
    except Exception:
        local.panel_call(opener, 'panel/api/clients/del/' + a['panel_key'], {}, csrf)
        raise
    after = panel(account)
    if not after['exists'] or not after['identity_matches'] or after['expiry_ms'] == 0:
        raise RuntimeError('Subscription operation unlimited fixture readback failed')
    return {'fixture': 'unlimited', 'after': after}


def fault_targets(account):
    a = account_row(account)
    if not a['panel_key'] or not a['assigned_panel_id']:
        raise RuntimeError('Subscription operation fault target requires assigned native client')
    paths = {name: '/panel/api/clients/' + suffix for name, suffix in {
      'lost_reset_success': 'resetTraffic/' + a['panel_key'],
      'partial_detach': a['panel_key'] + '/detach',
      'unavailable_read': 'get/' + a['panel_key'],
      'lost_update_success': 'update/' + a['panel_key']}.items()}
    path = write_private('fault-target-' + uuid4().hex + '.json',
                         {'account_id': uid(account), 'paths': paths,
                          'panel_key': a['panel_key'], 'identity_digest': panel(account)['identity_digest']})
    return {'descriptor': str(path), 'path_digests': {
        name: sha256(value.encode()).hexdigest() for name, value in paths.items()}}


def probe_config(account, email, password):
    a = account_row(account)
    opener = local.session()
    status, _, logged = local.api(opener, '/api/v1/auth/login',
                                  {'email': email, 'password': password})
    if status != 200 or logged['account']['account_id'] != uid(account):
        raise RuntimeError('Subscription operation probe identity login failed')
    status, _, result = local.api(opener, '/api/v1/subscription/key')
    if status != 200:
        raise RuntimeError('Subscription operation probe subscription key absent')
    _, _, raw = local.request(local.session(), result['subscription_url'])
    links = base64.b64decode(raw).decode().splitlines()
    if len(links) != 1:
        raise RuntimeError('Subscription operation probe subscription shape changed')
    link = urlsplit(links[0])
    args = parse_qs(link.query)
    if link.scheme != 'vless' or args.get('security') != ['tls'] or args.get('flow') != ['xtls-rprx-vision']:
        raise RuntimeError('Subscription operation probe VLESS format changed')
    if link.username != a['vpn_id']:
        raise RuntimeError('Subscription operation probe native identity mismatch')
    config = {'log': {'loglevel': 'none'},
      'inbounds': [{'listen': '0.0.0.0', 'port': 1080, 'protocol': 'http'}],
      'outbounds': [{'protocol': 'vless', 'settings': {'vnext': [{
        'address': 'panel', 'port': link.port,
        'users': [{'id': link.username, 'encryption': 'none', 'flow': args['flow'][0]}]}]},
        'streamSettings': {'network': 'tcp', 'security': 'tls', 'tlsSettings': {
          'serverName': 'panel', 'disableSystemRoot': True,
          'certificates': [{'certificateFile': '/run/secrets/public_cert', 'usage': 'verify'}]}}}]}
    path = write_private('probe-' + uuid4().hex + '.json', config)
    return {'config_path': str(path), 'config_digest': sha256(path.read_bytes()).hexdigest(),
            'allocated_identity_digest': sha256(json.dumps(
                [a['panel_key'], a['vpn_id'], a['sub_id']],
                separators=(',', ':')).encode()).hexdigest()}


def probe_requests(count):
    if type(count) is not int or not 1 <= count <= 30:
        raise ValueError('bounded Subscription operation probe requests required')
    proxy = urllib.request.build_opener(urllib.request.ProxyHandler({'http': 'http://127.0.0.1:59449'}))
    for _ in range(count):
        with proxy.open('http://origin:8000/', timeout=5) as response:
            if response.read() != local.VPN_ORIGIN_MARKER:
                raise RuntimeError('Subscription operation VPN probe wrong origin')
    return {'requests': count}


def restore_state(db=None):
    raw = query("""SELECT json_build_object(
      'operations',(SELECT count(*) FROM access_operations),
      'applied',(SELECT count(*) FROM access_operations WHERE status='applied'),
      'review',(SELECT count(*) FROM access_operations WHERE status='needs_review'),
      'requested',(SELECT count(*) FROM audit_events WHERE action='access_requested'),
      'completed',(SELECT count(*) FROM audit_events WHERE action='access_applied'),
      'operation_digest',coalesce((SELECT md5(string_agg(md5(to_jsonb(o)::text),',' ORDER BY o.id))
          FROM access_operations o),'empty'),
      'audit_digest',coalesce((SELECT md5(string_agg(md5(to_jsonb(e)::text),',' ORDER BY e.id))
          FROM audit_events e WHERE e.access_operation_id IS NOT NULL),'empty'),
      'catalogue_digest',coalesce((SELECT md5(string_agg(md5(to_jsonb(r)::text),','
          ORDER BY r.plan_id,r.revision)) FROM catalogue_revisions r),'empty'));
    """, db=db)
    return json.loads(raw)


def evidence(name, expected, actual, ok):
    STATE.mkdir(parents=True, exist_ok=True, mode=0o700)
    STATE.chmod(0o700)
    path = STATE / 'restore.jsonl'
    with path.open('a') as stream:
        stream.write(json.dumps({'criterion': name,
          'target': f'{local.PROJECT} own PostgreSQL and fresh disposable restore DB',
          'command': 'python3 deploy/subscription-operations/local.py restore',
          'expected': expected, 'actual': actual,
          'verdict': 'PASS' if ok else 'FAIL',
          'artifacts': [str(path)]}) + '\n')
    path.chmod(0o600)
    if not ok:
        raise RuntimeError('Subscription operation restore assertion failed')


def restore_acceptance():
    before = restore_state()
    if before['operations'] < 1:
        raise RuntimeError('Subscription operation restore requires previously applied own operations')
    name = local.restore_name('subscription-operations')
    # restrictions_bridge.query validates the same database() pointer as the active owned stack.
    query('CREATE DATABASE ' + name + ';', db='postgres')
    dump = local.compose('exec', '-T', 'postgres', 'pg_dump', '-U', local.PG_USER,
                         '-Fc', '-d', local.database())
    local.compose('exec', '-T', 'postgres', 'pg_restore', '-U', local.PG_USER,
                  '--no-owner', '--no-privileges', '-d', name, stdin=dump)
    restored = restore_state(name)
    equal = before == restored
    evidence('AC8 Subscription operation own DB backup/restore',
      'all access operations, referenced audit and catalogue digests preserved in fresh disposable DB',
      {'equal': equal, 'operations': restored['operations'],
       'applied': restored['applied'], 'review': restored['review'],
       'requested': restored['requested'], 'completed': restored['completed']}, equal)
    cleanup = (ROOT / 'backend/db/maintenance/post_restore_auth.sql').read_text()
    query(cleanup, db=name)
    second = query(cleanup, db=name)
    residual = query("""SELECT count(*) FROM sessions;
      SELECT count(*) FROM credential_challenges WHERE NOT revoked AND used_at IS NULL;
      SELECT count(*) FROM mail_deliveries WHERE kind='credential' AND ciphertext IS NOT NULL;""", db=name)
    after = restore_state(name)
    ok = second == '0\n0\n0' and residual == '0\n0\n0' and after == restored
    evidence('AC8 Subscription operation restored authentication cleanup',
      'repeat cleanup zeros sessions/proofs/credential ciphertext without altering access/catalogue history',
      {'second_zero': second == '0\n0\n0', 'residual_zero': residual == '0\n0\n0',
       'access_catalogue_equal': after == restored}, ok)
    return {'checks': 2, 'passed': 2}


def run(data):
    action = data['action']
    if action == 'transport':
        return restrictions_bridge.transport()
    if action in ('grant', 'revoke'):
        owned(data['account'])
        return {'role': restrictions_bridge.role(action, uid(data['account']))}
    if action == 'snapshot':
        return snapshot(data['account'])
    if action == 'operation':
        return operation(data['operation'])
    if action == 'fixture':
        return fixture(data['account'], data['kind'])
    if action == 'recovery-expire':
        return recovery_expire(data['account'])
    if action == 'fixture-unlimited':
        return fixture_unlimited(data['account'], data['reference_account'])
    if action == 'fault-targets':
        return fault_targets(data['account'])
    if action == 'probe-config':
        return probe_config(data['account'], data['email'], data['password'])
    if action == 'probe-requests':
        return probe_requests(data['count'])
    if action == 'restore':
        return restore_acceptance()
    raise ValueError('unknown Subscription operation bridge action')


if __name__ == '__main__':
    try:
        print(json.dumps(run(json.load(sys.stdin)), separators=(',', ':')))
    except Exception:
        print(json.dumps({'error': 'Subscription operation local bridge failed'}))
        sys.exit(1)
