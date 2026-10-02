"""Private, redacted bridge for S48 acceptance on the owned local Compose project."""
import importlib.util
from hashlib import sha256
import json
import os
from pathlib import Path
import secrets
import sqlite3
import subprocess
import sys
from uuid import UUID
from uuid import uuid4

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('s01_local', ROOT / 'deploy/s01/local.py')
local = importlib.util.module_from_spec(spec)
spec.loader.exec_module(local)
STATE = ROOT / '.superpowers/sdd/2026-10-02-s48-account-restrictions/e2e'


def own_uuid(value):
    return str(UUID(value))


def query(statement, db=None, **ids):
    variables = ''.join("\\set %s '%s'\n" % (name, own_uuid(value)) for name, value in ids.items())
    database = db or local.database()
    if database not in (local.database(), 'postgres') and not database.startswith('cabinet_s01_restore_'):
        raise ValueError('database outside owned project')
    return local.compose('exec', '-T', 'postgres', 'psql', '-U', 'cabinet_s01',
                         '-d', database, '-qAt', '-v', 'ON_ERROR_STOP=1',
                         stdin=(variables + statement).encode()).decode().strip()


def role(action, account):
    if action not in ('grant', 'revoke'):
        raise ValueError('invalid role action')
    STATE.mkdir(mode=0o700, parents=True, exist_ok=True)
    STATE.chmod(0o700)
    path = STATE / 'operator-account'
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, 'w') as stream:
        stream.write(own_uuid(account) + '\n')
    try:
        local.compose('run', '--rm', '--no-deps', '-T',
                      '--user', str(os.getuid()) + ':' + str(os.getgid()),
                      '-v', str(path) + ':/run/secrets/operator_account:ro',
                      'backend', 'operator', action, '--account-file', '/run/secrets/operator_account')
    finally:
        path.unlink(missing_ok=True)
    return query("SELECT EXISTS(SELECT 1 FROM operator_accounts WHERE account_id=:'account'::uuid);", account=account) == 't'


def state(account, db=None):
    raw = query("""SELECT json_build_object(
      'restricted',a.restricted,'vpn_banned',a.vpn_banned,'kind',a.kind,
      'operator',EXISTS(SELECT 1 FROM operator_accounts r WHERE r.account_id=a.id),
      'identity',md5(jsonb_build_object('vpn_id',a.vpn_id,'sub_id',a.sub_id,
        'panel_key',a.panel_key,'assigned_panel_id',a.assigned_panel_id,
        'had_subscription',a.had_subscription,'telegram_id',a.telegram_id,
        'legacy_user_id',a.legacy_user_id)::text),
      'operations',coalesce((SELECT md5(jsonb_agg(to_jsonb(o) ORDER BY o.id)::text)
        FROM trial_operations o WHERE o.account_id=a.id),'none'),
      'sessions',(SELECT count(*) FROM sessions x WHERE x.account_id=a.id),
      'proofs',(SELECT count(*) FROM credential_challenges x WHERE x.account_id=a.id AND NOT x.revoked AND x.used_at IS NULL),
      'proof_mail',(SELECT count(*) FROM mail_deliveries m JOIN credential_challenges c ON c.id=m.credential_challenge_id
        WHERE c.account_id=a.id AND m.kind='credential' AND m.ciphertext IS NOT NULL),
      'audit_restricted',(SELECT count(*) FROM audit_events e WHERE e.account_id=a.id AND e.action='account_restricted'),
      'audit_unrestricted',(SELECT count(*) FROM audit_events e WHERE e.account_id=a.id AND e.action='account_unrestricted'),
      'legacy_snapshot',(SELECT count(*) FROM legacy_approval_snapshots l WHERE l.account_id=a.id),
      'legacy_events',(SELECT count(*) FROM legacy_approval_events l WHERE l.account_id=a.id))
      FROM accounts a WHERE a.id=:'account'::uuid;""", db=db, account=account)
    if not raw:
        raise RuntimeError('owned account absent')
    return json.loads(raw)


def transport():
    config = local.ENV.read_text()
    return {'no_telegram_operators': 'BOT_OPERATOR_IDS=\n' in config,
            'bot_stopped': local.compose('ps', '-q', 'bot').decode().strip() == '',
            'reconcile_stopped': local.compose('ps', '-q', 'reconcile').decode().strip() == '',
            'vpn_connected': local.vpn_connected(),
            'project': local.PROJECT}


def record(rows, criterion, expected, actual, pass_):
    path = STATE / 'import.jsonl'
    with path.open('a') as output:
        output.write(json.dumps({'criterion': criterion,
            'target': 'cabinet-s01-local own PostgreSQL and synthetic read-only SQLite snapshot',
            'command': 'python3 deploy/s48/local.py import-acceptance',
            'expected': expected, 'actual': actual,
            'verdict': 'PASS' if pass_ else 'FAIL', 'artifacts': [str(path)]}) + '\n')
    rows.append(pass_)
    if not pass_:
        raise RuntimeError('acceptance assertion failed')


def import_cli(payload, flag):
    args = ['docker', 'compose', '--project-name', local.PROJECT, '--profile', 'restore',
            '--env-file', str(local.ENV), '-f', 'deploy/s01/compose.acceptance.yml',
            '-f', 'deploy/s01/compose.local.yml', 'run', '--rm', '--no-deps', '-T',
            'backend', 'import-legacy-approvals', flag]
    result = subprocess.run(args, cwd=ROOT, input=payload, capture_output=True, timeout=180)
    try:
        report = json.loads(result.stdout)
    except ValueError:
        raise RuntimeError('import CLI returned no structured report') from None
    if result.returncode == 0 and set(report) == {'users', 'events', 'changed'}:
        return report
    if result.returncode != 0 and set(report) == {'error'}:
        return report
    raise RuntimeError('import CLI returned invalid redacted report')


def import_acceptance():
    STATE.mkdir(mode=0o700, parents=True, exist_ok=True)
    STATE.chmod(0o700)
    rows = []
    fixture = STATE / ('synthetic-legacy-' + uuid4().hex + '.sqlite3')
    # Synthetic IDs are checked against the owned DB before insertion.
    tg = [secrets.randbelow(3_000_000_000_000_000) + 5_000_000_000_000_000 for _ in range(3)]
    if len(set(tg)) != 3 or any(query('SELECT count(*) FROM accounts WHERE telegram_id=' + str(x) + ';') != '0' for x in tg):
        raise RuntimeError('synthetic identity collision')
    ids = [str(uuid4()) for _ in tg]
    vpn = [str(uuid4()) for _ in tg]
    sub = [secrets.token_hex(8) for _ in tg]
    panel = ['s48_'+secrets.token_hex(12) for _ in tg]
    insert = []
    for index in range(3):
        insert.append("INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,restricted,vpn_id,sub_id,panel_key,terms_version,privacy_version,telegram_id,legacy_user_id,kind,display_name) VALUES ('%s',NULL,'en',NULL,NULL,false,'%s','%s','%s',NULL,NULL,%d,%d,'telegram','S48 synthetic %d');" %
                      (ids[index], vpn[index], sub[index], panel[index], tg[index], tg[index]+10_000_000, index))
    query('BEGIN;\n' + '\n'.join(insert) + '\nCOMMIT;')
    with sqlite3.connect(fixture) as db:
        db.execute('CREATE TABLE users(id INTEGER PRIMARY KEY,tg_id INTEGER,approval_status TEXT,approval_requested_at TEXT,approval_decided_at TEXT,approval_decided_by INTEGER)')
        db.execute('CREATE TABLE audit_log(id INTEGER PRIMARY KEY,created_at TEXT,actor_type TEXT,actor_id INTEGER,actor_name TEXT,action TEXT,target_id INTEGER,source TEXT)')
        for index,status in enumerate(('pending','rejected','approved')):
            db.execute('INSERT INTO users VALUES (?,?,?,?,?,?)',
                (tg[index]+10_000_000,tg[index],status,None if index != 2 else '2026-10-01 10:00:00',
                 None if index == 0 else '2026-10-01 11:00:00',None if index != 2 else 101))
        for index in range(52):
            db.execute('INSERT INTO audit_log VALUES (?,?,?,?,?,?,?,?)',
                (tg[1]+20_000_000+index,'2026-10-01 12:%02d:00' % index,
                 None if index == 0 else 'telegram',None if index == 0 else 101,
                 None if index == 0 else 'Synthetic operator',
                 'approval.reject' if index % 2 == 0 else 'approval.approve',tg[1],'main_bot'))
        db.execute('INSERT INTO audit_log VALUES (?,?,?,?,?,?,?,?)',
                   (tg[1]+30_000_000,'2026-10-01 13:00:00','telegram',101,
                    'Synthetic operator','support.reply',tg[1],'main_bot'))
    fixture.chmod(0o600)
    before = sha256(fixture.read_bytes()).hexdigest()
    export = subprocess.run(['python3', 'deploy/s48/export_legacy_approval.py', str(fixture),
                             '--timezone', 'Europe/Moscow'], cwd=ROOT, capture_output=True, timeout=30)
    if export.returncode or not export.stdout:
        raise RuntimeError('read-only exporter failed')
    package = json.loads(export.stdout)
    pending = next(row for row in package['users'] if row['status'] == 'pending')
    record(rows, 'AC5 read-only export', '3 source statuses, 52 approval events, NULL metadata retained; SQLite unchanged',
           'users=%d, events=%d, null actor=%s, byte hash equal=%s' %
           (len(package['users']),len(package['approval_events']),package['approval_events'][0]['actor_id'] is None,
            sha256(fixture.read_bytes()).hexdigest() == before),
           len(package['users']) == 3 and len(package['approval_events']) == 52 and
           pending['requested_at'] is None and package['approval_events'][0]['actor_id'] is None and
           sha256(fixture.read_bytes()).hexdigest() == before)
    pristine = [state(id) for id in ids]
    dry = import_cli(export.stdout, '--dry-run')
    after_dry = [state(id) for id in ids]
    record(rows, 'AC5 dry-run', 'valid whole package counted with zero PG writes',
           'report='+json.dumps(dry,sort_keys=True)+', unchanged='+(str(after_dry == pristine)),
           dry == {'users':3,'events':52,'changed':1} and after_dry == pristine)
    invalid_codes = []
    for change in ('version','unknown','duplicate','date'):
        invalid = json.loads(export.stdout)
        if change == 'version':
            invalid['version'] = 2
        elif change == 'unknown':
            invalid['unexpected'] = True
        elif change == 'duplicate':
            invalid['approval_events'].append(invalid['approval_events'][0])
        else:
            invalid['approval_events'][0]['created_at'] = 'not-an-instant'
        invalid_codes.append(import_cli(json.dumps(invalid).encode(), '--dry-run').get('error'))
    record(rows, 'AC5 invalid package boundary', 'unknown version/field, duplicate event and invalid date all rejected before write',
           'codes='+str(invalid_codes)+', unchanged='+(str([state(id) for id in ids] == pristine)),
           invalid_codes == ['IMPORT_INVALID_PACKAGE']*4 and [state(id) for id in ids] == pristine)
    applied = import_cli(export.stdout, '--apply')
    after_apply = [state(id) for id in ids]
    metadata = json.loads(query("""SELECT json_build_object(
      'pending_null',EXISTS(SELECT 1 FROM legacy_approval_snapshots WHERE account_id=:'pending'::uuid
        AND status='pending' AND requested_at IS NULL AND decided_at IS NULL AND decided_by IS NULL),
      'approved_actor',EXISTS(SELECT 1 FROM legacy_approval_snapshots WHERE account_id=:'approved'::uuid
        AND status='approved' AND decided_by=101 AND requested_at IS NOT NULL AND decided_at IS NOT NULL),
      'rejected_null',EXISTS(SELECT 1 FROM legacy_approval_snapshots WHERE account_id=:'rejected'::uuid
        AND status='rejected' AND requested_at IS NULL AND decided_by IS NULL),
      'event_null',EXISTS(SELECT 1 FROM legacy_approval_events WHERE account_id=:'rejected'::uuid
        AND actor_type IS NULL AND actor_id IS NULL AND actor_name IS NULL),
      'event_actor',EXISTS(SELECT 1 FROM legacy_approval_events WHERE account_id=:'rejected'::uuid
        AND actor_type='telegram' AND actor_id=101 AND actor_name='Synthetic operator' AND source='main_bot'),
      'source_ids',EXISTS(SELECT 1 FROM legacy_approval_events WHERE account_id=:'rejected'::uuid
        AND source_id=%d AND target_tg_id=%d) AND EXISTS(SELECT 1 FROM legacy_approval_events
        WHERE account_id=:'rejected'::uuid AND source_id=%d AND target_tg_id=%d),
      'nonapproval_absent',NOT EXISTS(SELECT 1 FROM legacy_approval_events WHERE action NOT IN ('approval.approve','approval.reject')));""" %
      (tg[1]+20_000_000,tg[1],tg[1]+20_000_051,tg[1]),
      pending=ids[0],approved=ids[2],rejected=ids[1]))
    record(rows, 'AC5 apply', 'pending/approved remain open, rejected restricted; all snapshots/events written once',
           'report='+json.dumps(applied,sort_keys=True)+', states='+str([x['restricted'] for x in after_apply])+', event count='+str(sum(x['legacy_events'] for x in after_apply))+', metadata flags='+str(metadata),
           applied == dry and [x['restricted'] for x in after_apply] == [False,True,False] and
           [x['legacy_snapshot'] for x in after_apply] == [1,1,1] and sum(x['legacy_events'] for x in after_apply) == 52 and all(metadata.values()))
    replay = import_cli(export.stdout, '--apply')
    after_replay = [state(id) for id in ids]
    record(rows, 'AC5 import replay', 'identical package no-op, no duplicate or state change',
           'report='+json.dumps(replay,sort_keys=True)+', unchanged='+(str(after_replay == after_apply)),
           replay == {'users':0,'events':0,'changed':0} and after_replay == after_apply)
    changed_package = json.loads(export.stdout)
    next(row for row in changed_package['users'] if row['status'] == 'approved')['status'] = 'rejected'
    changed = import_cli(json.dumps(changed_package).encode(), '--apply')
    unknown_package = json.loads(export.stdout)
    next(row for row in unknown_package['users'] if row['status'] == 'approved')['source_tg_id'] += 100_000_000
    unknown = import_cli(json.dumps(unknown_package).encode(), '--apply')
    after_conflict = [state(id) for id in ids]
    record(rows, 'AC5 conflict rollback', 'changed snapshot and unknown identity reject whole package',
           'codes='+str([changed.get('error'),unknown.get('error')])+', unchanged='+(str(after_conflict == after_apply)),
           changed.get('error') == 'IMPORT_SOURCE_CONFLICT' and unknown.get('error') == 'IMPORT_IDENTITY_CONFLICT' and
           after_conflict == after_apply)
    identity_errors = []
    for replacement in ('NULL', str(tg[2]+10_000_001)):
        try:
            query('UPDATE accounts SET legacy_user_id=' + replacement + " WHERE id=:'account'::uuid;", account=ids[2])
            identity_errors.append(import_cli(export.stdout, '--apply').get('error'))
        finally:
            query('UPDATE accounts SET legacy_user_id=' + str(tg[2]+10_000_000) + " WHERE id=:'account'::uuid;", account=ids[2])
    after_identity = [state(id) for id in ids]
    record(rows, 'AC5 matching source identity', 'NULL and wrong legacy_user_id fail whole import, original identity and records restored',
           'codes='+str(identity_errors)+', unchanged='+(str(after_identity == after_apply)),
           identity_errors == ['IMPORT_IDENTITY_CONFLICT','IMPORT_IDENTITY_CONFLICT'] and after_identity == after_apply)
    # The operator decision is made through the real HTTP API by browser.mjs.
    manifest = STATE / 'manifest.json'
    manifest.write_text(json.dumps({'rejected_account': ids[1], 'fixture': fixture.name}))
    manifest.chmod(0o600)
    dump = local.compose('exec', '-T', 'postgres', 'pg_dump', '-U', 'cabinet_s01', '-Fc', '-d', local.database())
    restored = 'cabinet_s01_restore_' + uuid4().hex
    query('CREATE DATABASE ' + restored + ';', db='postgres')
    local.compose('exec', '-T', 'postgres', 'pg_restore', '-U', 'cabinet_s01', '--no-owner', '--no-privileges', '-d', restored, stdin=dump)
    clone = [state(id, restored) for id in ids]
    record(rows, 'AC7 disposable restore', 'pg_dump/pg_restore into own disposable DB retains restriction/snapshots/events',
           'row state equal='+(str(clone == after_apply))+', restore DB created=true',clone == after_apply)
    maintenance = (ROOT / 'backend/db/maintenance/post_restore_auth.sql').read_text()
    first = query(maintenance, db=restored)
    second = query(maintenance, db=restored)
    clean = query("SELECT count(*) FROM sessions; SELECT count(*) FROM credential_challenges WHERE NOT revoked AND used_at IS NULL; SELECT count(*) FROM mail_deliveries WHERE kind='credential' AND ciphertext IS NOT NULL;", db=restored)
    record(rows, 'AC7 restore auth cleanup', 'existing S02/S06 SQL revokes sessions/proofs and ciphertext, second pass zero',
           'first counts='+str(first.splitlines())+', second counts='+str(second.splitlines())+', residual='+str(clean.splitlines()),
           second == '0\n0\n0' and clean == '0\n0\n0' and [state(id,restored)['restricted'] for id in ids] == [False,True,False])
    record(rows, 'AC5 source integrity', 'SQLite source remains byte-identical after export/import/restore',
           'SHA256 equal='+(str(sha256(fixture.read_bytes()).hexdigest() == before)),
           sha256(fixture.read_bytes()).hexdigest() == before)
    return {'checks': len(rows), 'passed': sum(rows)}


def run(data):
    action = data['action']
    if action in ('grant', 'revoke'):
        return {'role': role(action, data['account'])}
    if action == 'state':
        return state(data['account'])
    if action == 'transport':
        return transport()
    if action == 'manifest':
        path = STATE / 'manifest.json'
        if not path.is_file():
            return {'ready': False}
        return {'ready': True, 'rejected_account': own_uuid(json.loads(path.read_text())['rejected_account'])}
    if action == 'reimport':
        name = json.loads((STATE / 'manifest.json').read_text())['fixture']
        if not name.startswith('synthetic-legacy-') or not name.endswith('.sqlite3') or '/' in name:
            raise RuntimeError('fixture path invalid')
        fixture = STATE / name
        if not fixture.is_file():
            raise RuntimeError('fixture absent')
        before = sha256(fixture.read_bytes()).hexdigest()
        export = subprocess.run(['python3', 'deploy/s48/export_legacy_approval.py', str(fixture),
                                 '--timezone', 'Europe/Moscow'], cwd=ROOT, capture_output=True, timeout=30)
        if export.returncode:
            raise RuntimeError('export failed')
        result = import_cli(export.stdout, '--apply')
        return {'report': result, 'source_unchanged': sha256(fixture.read_bytes()).hexdigest() == before}
    raise ValueError('unknown action')


if __name__ == '__main__':
    try:
        if sys.argv[1:] == ['import-acceptance']:
            print(json.dumps(import_acceptance()))
        elif len(sys.argv) == 1:
            print(json.dumps(run(json.load(sys.stdin))))
        else:
            raise ValueError('unknown invocation')
    except Exception:
        if sys.argv[1:] == ['import-acceptance']:
            STATE.mkdir(mode=0o700, parents=True, exist_ok=True)
            STATE.chmod(0o700)
            path = STATE / 'import.jsonl'
            with path.open('a') as output:
                output.write(json.dumps({'criterion': 'S48 import driver interruption',
                    'target': 'cabinet-s01-local own PostgreSQL and synthetic SQLite',
                    'command': 'python3 deploy/s48/local.py import-acceptance',
                    'expected': 'all reached checks complete',
                    'actual': 'interrupted; inspect private bounded run log',
                    'verdict': 'BLOCKED', 'artifacts': [str(path)]}) + '\n')
        print('S48 local bridge failed', file=sys.stderr)
        raise SystemExit(1)
