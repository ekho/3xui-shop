"""S09 catalogue acceptance bridge for controlled fixtures in the owned stack."""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
from urllib.parse import urlsplit, urlunsplit
from uuid import UUID, uuid4

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('s48_local', ROOT / 'deploy/s48/local.py')
s48 = importlib.util.module_from_spec(spec)
spec.loader.exec_module(s48)
STATE = ROOT / '.superpowers/sdd/2026-10-02-s09-catalogue/e2e'


def own_uuid(value):
    return str(UUID(value))


def query(statement, db=None, **identifiers):
    return s48.query(statement, db=db, **identifiers)


def current(db=None):
    """Only counts and opaque digests leave the database helper."""
    raw = query("""SELECT json_build_object(
      'plans',(SELECT count(*) FROM catalogue_plans),
      'active',(SELECT count(*) FROM catalogue_plans WHERE NOT archived),
      'visible',(SELECT count(*) FROM catalogue_plans WHERE NOT archived AND NOT current_hidden),
      'revisions',(SELECT count(*) FROM catalogue_revisions),
      'history_digest',coalesce((SELECT md5(string_agg(
        r.plan_id::text || ':' || r.revision::text || ':' || md5(r.terms::text) ||
        ':' || r.archived::text || ':' || r.source || ':' ||
        coalesce(r.actor_account_id::text,'NULL') || ':' || coalesce(r.reason,'NULL'),
        ',' ORDER BY r.plan_id,r.revision))
        FROM catalogue_revisions r),'empty'),
      'digest',coalesce((SELECT md5(string_agg(
        p.id::text || ':' || p.current_revision::text || ':' || p.archived::text ||
        ':' || md5(r.terms::text),',' ORDER BY p.id))
        FROM catalogue_plans p JOIN catalogue_revisions r
          ON r.plan_id=p.id AND r.revision=p.current_revision),'empty'));""", db=db)
    return json.loads(raw)


def revisions(plan, db=None):
    raw = query("""SELECT coalesce(json_agg(json_build_object(
      'revision',r.revision,'terms_digest',md5(r.terms::text),
      'archived',r.archived,'source',r.source,
      'has_actor',r.actor_account_id IS NOT NULL,'has_reason',r.reason IS NOT NULL)
      ORDER BY r.revision),'[]'::json)
      FROM catalogue_revisions r WHERE r.plan_id=:'plan'::uuid;""", db=db, plan=own_uuid(plan))
    return json.loads(raw)


def evidence(criterion, expected, actual, passed):
    STATE.mkdir(mode=0o700, parents=True, exist_ok=True)
    STATE.chmod(0o700)
    path = STATE / 'local.jsonl'
    with path.open('a') as output:
        output.write(json.dumps({
            'criterion': criterion,
            'target': 'cabinet-s01-local own PostgreSQL and disposable restore databases; pinned backend image',
            'command': 'python3 deploy/s09/local.py import-acceptance',
            'expected': expected, 'actual': actual,
            'verdict': 'PASS' if passed else 'FAIL',
            'artifacts': [str(path)]}) + '\n')
    path.chmod(0o600)
    if not passed:
        raise RuntimeError('S09 acceptance assertion failed')


def database_file(db):
    if not db.startswith('cabinet_s01_restore_s09_'):
        raise ValueError('disposable database name required')
    source = urlsplit((s48.local.STATE / 'database-url').read_text().strip())
    if source.hostname != 'postgres' or source.username != 'cabinet_s01' or source.path != '/' + s48.local.database():
        raise RuntimeError('owned database URL mismatch')
    STATE.mkdir(mode=0o700, parents=True, exist_ok=True)
    STATE.chmod(0o700)
    path = STATE / ('database-url-' + uuid4().hex)
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as stream:
        stream.write(urlunsplit(source._replace(path='/' + db)) + '\n')
    return path


def backend_command(parts, payload=None, db=None):
    args = ['docker', 'compose', '--project-name', s48.local.PROJECT, '--profile', 'restore',
            '--env-file', str(s48.local.ENV), '-f', 'deploy/s01/compose.acceptance.yml',
            '-f', 'deploy/s01/compose.local.yml', 'run', '--rm', '--no-deps', '-T']
    path = database_file(db) if db is not None else None
    if path is not None:
        args += ['-e', 'DATABASE_URL_FILE=/run/secrets/s09_database_url',
                 '-v', str(path) + ':/run/secrets/s09_database_url:ro']
    args += ['backend', *parts]
    try:
        result = subprocess.run(args, cwd=ROOT, input=payload, capture_output=True, timeout=180)
        if parts == ['migrate']:
            if result.returncode:
                raise RuntimeError('disposable migration failed')
            return {'migrated': True}
        try:
            report = json.loads(result.stdout)
        except ValueError:
            raise RuntimeError('catalogue CLI did not return JSON') from None
        if result.returncode == 0 and 'error' not in report:
            return report
        if result.returncode != 0 and set(report) == {'error'}:
            return report
        raise RuntimeError('catalogue CLI returned unexpected report')
    finally:
        if path is not None:
            path.unlink(missing_ok=True)


def seed_main():
    result = backend_command(['catalogue', 'seed-unlimited'])
    if set(result) != {'plan_id', 'revision', 'created'}:
        raise RuntimeError('seed returned unexpected shape')
    plan = own_uuid(result['plan_id'])
    metadata = json.loads(query("""SELECT json_build_object(
      'hidden',p.current_hidden,'profile',p.current_profile,
      'devices',p.current_devices,'traffic_gb',r.terms->>'traffic_gb',
      'source',r.source,'actor_null',r.actor_account_id IS NULL,
      'reason_null',r.reason IS NULL)
      FROM catalogue_plans p JOIN catalogue_revisions r ON r.plan_id=p.id
        AND r.revision=p.current_revision WHERE p.id=:'plan'::uuid;""", plan=plan))
    return {'plan_id': plan, 'revision': result['revision'], 'created': result['created'], 'metadata': metadata}


def import_acceptance():
    """Controlled synthetic package against a fresh DB in the owned PG container."""
    passed = []
    def check(name, expected, actual, ok):
        evidence(name, expected, actual, ok)
        passed.append(ok)

    name = 'cabinet_s01_restore_s09_' + uuid4().hex
    query('CREATE DATABASE ' + name + ';', db='postgres')
    backend_command(['migrate'], db=name)
    empty = current(name)
    check('AC6 disposable empty catalogue',
          'fresh migrated DB has no plans or revisions',
          {'plans': empty['plans'], 'revisions': empty['revisions']},
          empty['plans'] == 0 and empty['revisions'] == 0)

    visible = {
        'legacy_plan_id': 701, 'devices': 8, 'traffic_gb': 0,
        'profile': 'regular', 'hidden': False,
        'prices': {
            'RUB': {'30': '90071992547409.93', '90': '0'},
            'USD': {'30': '123.45', '90': '999.99'},
            'XTR': {'30': '0', '90': '10'}}}
    hidden = {
        'legacy_plan_id': 702, 'devices': 9, 'traffic_gb': 100,
        'profile': 'euru', 'hidden': True, 'prices': {}}
    package = {'version': 1, 'durations': [30, 90], 'plans': [visible, hidden]}
    payload = json.dumps(package, separators=(',', ':')).encode()
    dry = backend_command(['catalogue', 'import-legacy', '--dry-run'], payload, db=name)
    after_dry = current(name)
    check('AC6 strict import dry-run',
          'version1 package counted with no committed DB change; exact major-unit decimals accepted',
          {'report': dry, 'unchanged': after_dry == empty},
          dry == {'created': 2, 'replayed': 0, 'dry_run': True} and after_dry == empty)
    applied = backend_command(['catalogue', 'import-legacy', '--apply'], payload, db=name)
    after_apply = current(name)
    metadata = json.loads(query("""SELECT json_build_object(
      'source_count',(SELECT count(*) FROM catalogue_revisions WHERE source='legacy_import'),
      'null_actor_reason',NOT EXISTS(SELECT 1 FROM catalogue_revisions
        WHERE source='legacy_import' AND (actor_account_id IS NOT NULL OR reason IS NOT NULL)),
      'exact_price',EXISTS(SELECT 1 FROM catalogue_plans p JOIN catalogue_revisions r
        ON r.plan_id=p.id AND r.revision=p.current_revision,
        jsonb_array_elements(r.terms->'prices') v
        WHERE p.legacy_plan_id=701 AND v->>'currency'='RUB'
          AND v->>'period_days'='30' AND v->>'amount_minor'='9007199254740993'));""", db=name))
    check('AC2/3/6 exact legacy import',
          'two plans/revisions imported, actor/reason NULL, exact >2^53 minor units retained',
          {'report': applied, 'plans': after_apply['plans'], 'revisions': after_apply['revisions'], **metadata},
          applied == {'created': 2, 'replayed': 0, 'dry_run': False} and
          after_apply['plans'] == 2 and after_apply['revisions'] == 2 and
          metadata == {'source_count': 2, 'null_actor_reason': True, 'exact_price': True})
    replay = backend_command(['catalogue', 'import-legacy', '--apply'], payload, db=name)
    after_replay = current(name)
    check('AC6 identical import replay',
          'same version1 package replays without duplicate revision or state change',
          {'report': replay, 'unchanged': after_replay == after_apply},
          replay == {'created': 0, 'replayed': 2, 'dry_run': False} and after_replay == after_apply)

    invalid = []
    for altered in (
        {**package, 'version': 2},
        {**package, 'unknown': True},
        {**package, 'plans': [{**visible, 'prices': {**visible['prices'], 'RUB': {'30': '1.234', '90': '0'}}}]},
        {**package, 'plans': [{**visible, 'prices': {**visible['prices'], 'XTR': {'30': '1.5', '90': '10'}}}]},
        {**package, 'plans': [{**visible, 'prices': {**visible['prices'], 'RUB': {'30': '92233720368547758.08', '90': '0'}}}]},
        {**package, 'plans': [visible, visible]},
    ):
        report = backend_command(['catalogue', 'import-legacy', '--apply'],
                                 json.dumps(altered).encode(), db=name)
        invalid.append(report.get('error'))
    after_invalid = current(name)
    check('AC2/6 invalid package boundary',
          'version/unknown field/fractional RUB/XTR/overflow/duplicate identity rejected atomically',
          {'codes': invalid, 'unchanged': after_invalid == after_apply},
          invalid == ['IMPORT_INVALID_PACKAGE'] * 6 and after_invalid == after_apply)

    mismatch = {**package, 'plans': [{**visible, 'devices': 10}]}
    changed = backend_command(['catalogue', 'import-legacy', '--apply'],
                              json.dumps(mismatch).encode(), db=name)
    occupied = {**package, 'plans': [
        {**hidden, 'legacy_plan_id': 703, 'devices': 10},
        {**hidden, 'legacy_plan_id': 704, 'devices': 8}]}
    conflict = backend_command(['catalogue', 'import-legacy', '--apply'],
                               json.dumps(occupied).encode(), db=name)
    after_conflict = current(name)
    check('AC4/6 mismatched identity and occupied slot rollback',
          'changed legacy ID terms conflict and occupied devices reject entire package',
          {'codes': [changed.get('error'), conflict.get('error')], 'unchanged': after_conflict == after_apply},
          changed.get('error') == 'IMPORT_CONFLICT' and
          conflict.get('error') == 'CATALOGUE_DEVICES_CONFLICT' and after_conflict == after_apply)

    slot = {**package, 'plans': [{**hidden, 'legacy_plan_id': 705, 'devices': 7}]}
    slot_result = backend_command(['catalogue', 'import-legacy', '--apply'],
                                  json.dumps(slot).encode(), db=name)
    before_seed_conflict = current(name)
    seed_conflict = backend_command(['catalogue', 'seed-unlimited'], db=name)
    check('AC6 unlimited seed respects occupied devices',
          'occupied devices=7 refuses seed without changing imported plan',
          {'slot_created': slot_result.get('created'), 'error': seed_conflict.get('error'),
           'unchanged': current(name) == before_seed_conflict},
          slot_result.get('created') == 1 and
          seed_conflict.get('error') == 'CATALOGUE_DEVICES_CONFLICT' and
          current(name) == before_seed_conflict)
    two_unlimited = {**package, 'plans': [
        {**hidden, 'legacy_plan_id': 706, 'devices': 10, 'profile': 'unlimited'},
        {**hidden, 'legacy_plan_id': 707, 'devices': 11, 'profile': 'unlimited'}]}
    unlimited_result = backend_command(['catalogue', 'import-legacy', '--apply'],
                                       json.dumps(two_unlimited).encode(), db=name)
    before_ambiguous = current(name)
    ambiguous = backend_command(['catalogue', 'seed-unlimited'], db=name)
    check('AC6 unlimited seed ambiguous source',
          'two distinct active unlimited plans refuse seed without overwrite',
          {'import_created': unlimited_result.get('created'), 'error': ambiguous.get('error'),
           'unchanged': current(name) == before_ambiguous},
          unlimited_result.get('created') == 2 and
          ambiguous.get('error') == 'CATALOGUE_UNLIMITED_AMBIGUOUS' and
          current(name) == before_ambiguous)

    main_before = current()
    restored = 'cabinet_s01_restore_s09_' + uuid4().hex
    dump = s48.local.compose('exec', '-T', 'postgres', 'pg_dump', '-U', 'cabinet_s01',
                             '-Fc', '-d', s48.local.database())
    query('CREATE DATABASE ' + restored + ';', db='postgres')
    s48.local.compose('exec', '-T', 'postgres', 'pg_restore', '-U', 'cabinet_s01',
                      '--no-owner', '--no-privileges', '-d', restored, stdin=dump)
    clone = current(restored)
    check('AC3/7 disposable backup restore',
          'pg_dump/pg_restore retains current catalogue, all revision counts and digest',
          {'state_equal': clone == main_before, 'plans': clone['plans'], 'revisions': clone['revisions']},
          clone == main_before)
    maintenance = (ROOT / 'backend/db/maintenance/post_restore_auth.sql').read_text()
    query(maintenance, db=restored)
    second = query(maintenance, db=restored)
    residual = query("""SELECT count(*) FROM sessions;
      SELECT count(*) FROM credential_challenges WHERE NOT revoked AND used_at IS NULL;
      SELECT count(*) FROM mail_deliveries WHERE kind='credential' AND ciphertext IS NOT NULL;""", db=restored)
    check('AC7 restore authentication cleanup',
          'C02/C06 cleanup repeat leaves zero sessions/proofs/ciphertext and catalogue equal',
          {'second_zero': second == '0\n0\n0', 'residual_zero': residual == '0\n0\n0',
           'catalogue_equal': current(restored) == main_before},
          second == '0\n0\n0' and residual == '0\n0\n0' and current(restored) == main_before)
    return {'checks': len(passed), 'passed': sum(passed)}


def run(data):
    action = data['action']
    if action == 'transport':
        return s48.transport()
    if action in ('grant', 'revoke'):
        return {'role': s48.role(action, own_uuid(data['account']))}
    if action == 'current':
        return current()
    if action == 'revisions':
        return revisions(data['plan'])
    if action == 'seed':
        return seed_main()
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
            path = STATE / 'local.jsonl'
            with path.open('a') as output:
                output.write(json.dumps({
                    'criterion': 'S09 local driver interruption',
                    'target': 'cabinet-s01-local disposable PostgreSQL',
                    'command': 'python3 deploy/s09/local.py import-acceptance',
                    'expected': 'all reached checks complete',
                    'actual': 'driver or runtime error; inspect private bounded run log',
                    'verdict': 'BLOCKED', 'artifacts': [str(path)]}) + '\n')
            path.chmod(0o600)
        print('S09 local bridge failed', file=sys.stderr)
        raise SystemExit(1)
