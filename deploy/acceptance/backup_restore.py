"""Backup: owned, disposable PostgreSQL/Redis fixture and real backup rehearsal."""
import argparse
import base64
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import time
from uuid import uuid4

ROOT = Path(__file__).resolve().parents[2]
STATE = ROOT / '.superpowers/acceptance/backup-restore'
PG_IMAGE = 'postgres@sha256:d74eeac9a635390a49bc21bd49fccd973de707e2a53a76ac49b552b8712ec46f'
REDIS_IMAGE = 'redis@sha256:6f81e8915c60b065a524e6967e0ad1c639ba6efa84d669f823683ea04d9150ee'


def command(args, *, data=None, timeout=180, allowed=(0,)):
    result = subprocess.run(args, cwd=ROOT, input=data, capture_output=True, timeout=timeout)
    if result.returncode not in allowed:
        raise RuntimeError(f'Backup command failed: {args[0]} (exit {result.returncode}); output withheld')
    return result


def metadata():
    value = json.loads((STATE / 'runtime.json').read_text())
    assert re.fullmatch(r'cabinet-[a-z][a-z0-9]*-[0-9a-f]{8}', value['project'])
    return value


def compose(*args, data=None, timeout=180):
    return command(['docker', 'compose', '-p', metadata()['project'], '-f', str(STATE / 'compose.json'), *args], data=data, timeout=timeout).stdout


def write(name, value):
    path = STATE / name
    path.write_text(value)
    path.chmod(0o600)
    return path


def pg(sql, database='platform_test'):
    assert re.fullmatch(r'(?:platform_test|backup_source|rehearsal_[a-z0-9_]+)', database)
    return compose('exec', '-T', 'postgres', 'psql', '-X', '-v', 'ON_ERROR_STOP=1', '-At', '-U', 'platform_test', '-d', database, data=sql.encode()).decode().strip()


def write_test_files():
    for service, port, secret in [('postgres', '5432', 'test-database-url'), ('redis', '6379', 'test-redis-url')]:
        address = compose('port', service, port).decode().strip()
        assert re.fullmatch(r'127\.0\.0\.1:[0-9]+', address)
        write(secret, ('postgres://platform_test@' + address + '/platform_test?sslmode=disable') if service == 'postgres' else ('redis://' + address + '/0'))
    container = compose('ps', '-q', 'postgres').decode().strip()
    assert re.fullmatch(r'[0-9a-f]{64}', container)
    write('test-postgres-fixture', json.dumps({'project': metadata()['project'], 'container_id': container}))


def up():
    if (STATE / 'runtime.json').exists():
        compose('up', '-d', '--wait', 'postgres', 'redis')
        write_test_files()
        print('PASS: existing owned Backup fixture ready')
        return
    STATE.mkdir(parents=True, mode=0o700, exist_ok=True)
    STATE.chmod(0o700)
    ids = command(['docker', 'network', 'ls', '-q']).stdout.decode().split()
    networks = json.loads(command(['docker', 'network', 'inspect', *ids]).stdout) if ids else []
    used = [ipaddress.ip_network(c['Subnet']) for n in networks for c in (n.get('IPAM', {}).get('Config') or []) if c.get('Subnet')]
    subnet = next((f'172.31.{i}.0/28' for i in range(140, 240) if not any(ipaddress.ip_network(f'172.31.{i}.0/28').overlaps(n) for n in used if n.version == 4)), None)
    assert subnet, 'No free dedicated Backup subnet; do not remove other networks'
    project = 'cabinet-backup-' + uuid4().hex[:8]
    write('runtime.json', json.dumps({'project': project, 'subnet': subnet}))
    configuration = {'name': project, 'services': {
        'postgres': {'image': PG_IMAGE, 'environment': {'POSTGRES_USER': 'platform_test', 'POSTGRES_DB': 'platform_test', 'POSTGRES_HOST_AUTH_METHOD': 'trust'},
                     'ports': ['127.0.0.1::5432'], 'volumes': ['pg:/var/lib/postgresql/data'],
                     'healthcheck': {'test': ['CMD', 'pg_isready', '-U', 'platform_test', '-d', 'platform_test'], 'interval': '2s', 'timeout': '2s', 'retries': 30}},
        'redis': {'image': REDIS_IMAGE, 'ports': ['127.0.0.1::6379'],
                  'healthcheck': {'test': ['CMD', 'redis-cli', 'ping'], 'interval': '2s', 'timeout': '2s', 'retries': 30}}},
        'volumes': {'pg': {}}, 'networks': {'default': {'ipam': {'config': [{'subnet': subnet}]}}}}
    write('compose.json', json.dumps(configuration))
    compose('up', '-d', '--wait', 'postgres', 'redis')
    write_test_files()
    print('PASS: isolated Backup fixture, dynamic loopback ports, private test URL files')


def down():
    compose('down', '-v', '--remove-orphans')
    shutil.rmtree(STATE)
    print('PASS: owned Backup containers/network/volume removed')


def check():
    (STATE / 'result.json').unlink(missing_ok=True)
    runtime = metadata()
    image = runtime['project'] + '-operations:local'
    command(['docker', 'build', '--target', 'operations', '-t', image, 'backend'], timeout=900)
    if not pg("SELECT 1 FROM pg_roles WHERE rolname='backup_operator'"):
        pg('CREATE ROLE backup_operator LOGIN CREATEDB NOSUPERUSER;')
    if not pg("SELECT 1 FROM pg_database WHERE datname='backup_source'"):
        pg('CREATE DATABASE backup_source OWNER backup_operator;')
    write('database-source', 'postgres://backup_operator@postgres:5432/backup_source?sslmode=disable')
    write('database-restore', 'postgres://backup_operator@postgres:5432/platform_test?sslmode=disable')
    write('redis-container', 'redis://redis:6379/0')
    for name in ('mail-key', 'code-key'):
        write(name, base64.b64encode(b'\x00' * 32).decode())

    def server_args(args, *, pause_dump=False, source='database-source', restore='database-restore'):
        env = {'DATABASE_URL_FILE': '/fixture/' + source, 'RESTORE_DATABASE_URL_FILE': '/fixture/' + restore,
               'REDIS_URL_FILE': '/fixture/redis-container', 'MAIL_KEY_FILE': '/fixture/mail-key', 'CODE_KEY_FILE': '/fixture/code-key',
               'CABINET_ORIGIN': 'https://backup.example.test', 'TERMS_VERSION': '1', 'PRIVACY_VERSION': '1', 'LEGACY_BOT_API_ENABLED': 'false'}
        if pause_dump:
            env['PATH'] = '/fixture/controlled-bin:/usr/lib/postgresql/17/bin:/usr/bin:/bin'
        command_args = ['docker', 'run', '--rm', '--network', runtime['project'] + '_default', '--user', f'{os.getuid()}:{os.getgid()}',
                        '--read-only', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges:true', '--tmpfs', '/tmp:mode=1777', '-v', f'{STATE}:/fixture']
        for name, value in env.items():
            command_args.extend(['-e', name + '=' + value])
        return [*command_args, image, *args]

    def server(args, *, success=True, code=None, source='database-source', restore='database-restore'):
        result = command(server_args(args, source=source, restore=restore), allowed=(0, 1), timeout=900)
        codes = re.findall(rb'code=([A-Z_]+)', result.stdout + result.stderr)
        assert (result.returncode == 0) == success, 'Unexpected backup result; stable codes: ' + ','.join(c.decode() for c in codes)
        if code:
            assert code.encode() in result.stdout + result.stderr, 'Expected stable Backup failure code missing'
        # This fixture contains no real credentials or user data; even test DSNs must stay out of output.
        assert b'postgres://' not in result.stdout + result.stderr
        return result

    server(['migrate'])
    operator, customer, foreign, conversation, first, second = (str(uuid4()) for _ in range(6))
    write('operator', operator)
    write('foreign', foreign)
    write('unknown', str(uuid4()))
    bytes_a = bytes(range(256)) * 1024
    bytes_b = b'synthetic attachment\x00\xff\n' * 23
    pg(f"""
INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version)
VALUES {','.join(f"('{id}','{id}@example.test','ru','fixture',now(),'{uuid4()}','{uuid4().hex[:16]}','acct_{id}','1','1')" for id in (operator, customer, foreign))};
INSERT INTO operator_accounts(account_id,granted_at) VALUES('{operator}',now());
INSERT INTO support_conversations(id,account_id,status,created_at,updated_at) VALUES('{conversation}','{customer}','open',now(),now());
INSERT INTO support_messages(id,conversation_id,sender_account_id,sender_kind,text,created_at,attachment_name,attachment_bytes)
VALUES('{first}','{conversation}','{customer}','customer','synthetic',now(),'fixture.bin',decode('{bytes_a.hex()}','hex')),
      ('{second}','{conversation}','{operator}','operator','synthetic reply',now(),'fixture.dat',decode('{bytes_b.hex()}','hex'));
""", 'backup_source')
    run = uuid4().hex
    package = 'package-' + run
    target = 'rehearsal_' + run

    def backup(directory=package, actor='operator', *, success=True, code=None):
        return server(['backup', 'create', '--operator-file', '/fixture/' + actor, '--directory', '/fixture/' + directory], success=success, code=code)

    def rehearse(directory=package, database=target, *, success=True, code=None):
        return server(['backup', 'rehearse', '--operator-file', '/fixture/operator', '--directory', '/fixture/' + directory, '--target', database], success=success, code=code)

    def restore_exists(database):
        assert re.fullmatch(r'rehearsal_[a-z0-9_]+', database)
        return pg(f"SELECT count(*) FROM pg_database WHERE datname='{database}'") == '1'

    for url in (
        'postgres://backup_operator@postgres,postgres:5432/backup_source?sslmode=disable',
        'postgres://backup_operator@postgres:5432/backup_source?sslmode=disable&channel_binding=require',
        'postgres://backup_operator@postgres:5432/backup_source?sslmode=disable&target_session_attrs=read-write',
        'postgres://backup_operator@postgres:5432/backup_source?sslmode=disable&sslmode=verify-full',
        'postgres://backup_operator@postgres:5432/backup_source?sslmode=verify-full',
        'postgres://backup_operator@postgres:5432/backup_source?sslmode=verify-ca',
    ):
        write('invalid-url', url)
        audit_before = pg('SELECT count(*) FROM audit_events', 'backup_source')
        directory, database = 'invalid-url-' + uuid4().hex, 'rehearsal_' + uuid4().hex
        server(['backup', 'create', '--operator-file', '/fixture/operator', '--directory', '/fixture/' + directory],
               source='invalid-url', success=False, code='INVALID_DATABASE_URL')
        server(['backup', 'rehearse', '--operator-file', '/fixture/operator', '--directory', '/fixture/' + directory, '--target', database],
               restore='invalid-url', success=False, code='INVALID_DATABASE_URL')
        assert not (STATE / directory).exists() and not restore_exists(database)
        assert pg('SELECT count(*) FROM audit_events', 'backup_source') == audit_before, 'Invalid URL reached audit side effects'
    print('PASS: unsupported/multi-host/repeated URL settings rejected on source/restore before side effects', flush=True)

    backup()
    original = STATE / package
    manifest = json.loads((original / 'manifest.json').read_text())
    assert set(p.name for p in original.iterdir()) == {'manifest.json', 'database.dump'}
    assert original.stat().st_mode & 0o777 == 0o700
    for p in original.iterdir():
        assert p.stat().st_mode & 0o777 == 0o600
    dump = (original / 'database.dump').read_bytes()
    assert dump.startswith(b'PGDMP') and hashlib.sha256(dump).hexdigest() in json.dumps(manifest)
    assert hashlib.sha256(bytes_a).hexdigest() in json.dumps(manifest)
    assert hashlib.sha256(bytes_b).hexdigest() in json.dumps(manifest)
    rehearse()
    assert restore_exists(target)
    assert pg(f"SELECT count(*) FROM accounts WHERE id IN ('{operator}','{customer}','{foreign}')", target) == '3'
    assert pg(f"SELECT count(*) FROM support_messages m JOIN support_conversations c ON c.id=m.conversation_id JOIN accounts a ON a.id=c.account_id WHERE c.id='{conversation}' AND a.id='{customer}'", target) == '2'
    assert pg(f"SELECT encode(sha256(attachment_bytes),'hex') FROM support_messages WHERE id='{first}'", target) == hashlib.sha256(bytes_a).hexdigest()
    assert pg(f"SELECT encode(sha256(attachment_bytes),'hex') FROM support_messages WHERE id='{second}'", target) == hashlib.sha256(bytes_b).hexdigest()
    assert pg(f"SELECT sequence FROM support_messages WHERE id='{first}'", target) == pg(f"SELECT sequence FROM support_messages WHERE id='{first}'", 'backup_source')
    print('PASS: real pg_dump/pg_restore, manifest, all logical inventories, binary bytes and linked identities', flush=True)

    for change, undo in (
        ('ALTER TABLE catalogue_revisions DISABLE TRIGGER catalogue_revision_immutable',
         'ALTER TABLE catalogue_revisions ENABLE TRIGGER catalogue_revision_immutable'),
        ("ALTER TYPE river_job_state RENAME VALUE 'cancelled' TO 'backup_cancelled'",
         "ALTER TYPE river_job_state RENAME VALUE 'backup_cancelled' TO 'cancelled'"),
    ):
        database = 'rehearsal_' + uuid4().hex
        pg(change, 'backup_source')
        try:
            rehearse(database=database, success=False, code='SCHEMA_MISMATCH')
            assert not restore_exists(database), 'Live trigger/enum drift reached CREATE DATABASE'
        finally:
            pg(undo, 'backup_source')
    print('PASS: current immutable-trigger state and River enum drift rejected before CREATE', flush=True)

    # Pause only the real pg_dump process, after the application has exported its snapshot and locked tables.
    control = STATE / 'controlled-bin'
    control.mkdir(mode=0o700, exist_ok=True)
    ready, release = STATE / 'snapshot-ready', STATE / 'release-dump'
    ready.unlink(missing_ok=True)
    release.unlink(missing_ok=True)
    (control / 'pg_dump').write_text('#!/bin/sh\n: > /fixture/snapshot-ready\nfor i in $(seq 1 300); do\n  [ -f /fixture/release-dump ] && exec /usr/lib/postgresql/17/bin/pg_dump "$@"\n  sleep 0.1\ndone\nexit 1\n')
    (control / 'pg_dump').chmod(0o700)
    concurrent_package = 'concurrent-' + uuid4().hex
    process = subprocess.Popen(server_args(['backup', 'create', '--operator-file', '/fixture/operator', '--directory', '/fixture/' + concurrent_package], pause_dump=True), cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    writer = None
    try:
        deadline = time.monotonic() + 30
        while not ready.exists() and process.poll() is None and time.monotonic() < deadline:
            time.sleep(0.1)
        assert ready.exists(), 'Backup did not reach exported snapshot'
        writer = subprocess.Popen(['docker', 'compose', '-p', runtime['project'], '-f', str(STATE / 'compose.json'), 'exec', '-T', '-e', 'PGAPPNAME=backup-fixture-writer', 'postgres', 'psql', '-X', '-v', 'ON_ERROR_STOP=1', '-At', '-U', 'platform_test', '-d', 'backup_source'], cwd=ROOT, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        writer.stdin.write(f"UPDATE support_messages SET text='after snapshot',attachment_bytes=decode('00112233','hex') WHERE id='{first}';\n".encode())
        writer.stdin.close()
        writer.stdin = None
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            if pg("SELECT count(*) FROM pg_stat_activity WHERE application_name='backup-fixture-writer' AND wait_event_type='Lock'", 'backup_source') == '1':
                break
            time.sleep(0.1)
        else:
            raise AssertionError('Concurrent attachment writer was not blocked by backup lock')
    finally:
        release.write_text('release')
    stdout, stderr = process.communicate(timeout=60)
    assert process.returncode == 0 and b'postgres://' not in stdout + stderr
    assert writer is not None
    writer.communicate(timeout=60)
    assert writer.returncode == 0
    concurrent_target = 'rehearsal_' + uuid4().hex
    rehearse(concurrent_package, concurrent_target)
    assert pg(f"SELECT encode(sha256(attachment_bytes),'hex') FROM support_messages WHERE id='{first}'", concurrent_target) == hashlib.sha256(bytes_a).hexdigest()
    assert pg(f"SELECT encode(attachment_bytes,'hex') FROM support_messages WHERE id='{first}'", 'backup_source') == '00112233'
    print('PASS: real concurrent writer waited on SHARE lock; snapshot retained old DB/file bytes, source advanced after release', flush=True)

    backup(success=False)
    rehearse(success=False)
    assert hashlib.sha256((original / 'database.dump').read_bytes()).hexdigest() == hashlib.sha256(dump).hexdigest()
    for actor in ('foreign', 'unknown'):
        directory = 'denied-' + uuid4().hex
        backup(directory, actor, success=False)
        assert not (STATE / directory).exists()
    for clause in ("DELETE FROM operator_accounts WHERE account_id", 'UPDATE accounts SET restricted=true WHERE id'):
        pg(f"{clause}='{operator}'", 'backup_source')
        directory = 'denied-' + uuid4().hex
        backup(directory, success=False)
        assert not (STATE / directory).exists()
        if clause.startswith('DELETE'):
            pg(f"INSERT INTO operator_accounts(account_id,granted_at) VALUES('{operator}',now())", 'backup_source')
        else:
            pg(f"UPDATE accounts SET restricted=false WHERE id='{operator}'", 'backup_source')
    print('PASS: unknown/customer/revoked/restricted roles denied; repeated create/restore preserved destinations', flush=True)

    def damaged(label, modify):
        directory = label + '-' + uuid4().hex
        destination = STATE / directory
        shutil.copytree(original, destination)
        modify(destination)
        database = 'rehearsal_' + uuid4().hex
        rehearse(directory, database, success=False)
        assert not restore_exists(database), 'Invalid package reached CREATE DATABASE'

    def alter_manifest(path, change):
        p = path / 'manifest.json'
        value = json.loads(p.read_text())
        change(value)
        p.write_text(json.dumps(value, indent=2, ensure_ascii=False))

    damaged('missing', lambda p: (p / 'manifest.json').unlink())
    damaged('truncated', lambda p: (p / 'manifest.json').write_text('{'))
    damaged('unknown-field', lambda p: alter_manifest(p, lambda v: v.update(unknown='rejected')))
    damaged('missing-field', lambda p: alter_manifest(p, lambda v: v.pop('tables')))
    damaged('schema', lambda p: alter_manifest(p, lambda v: v['schema'].update(version=999)))
    damaged('postgres-major', lambda p: alter_manifest(p, lambda v: v.update(pg_major=999)))
    damaged('manifest-size', lambda p: alter_manifest(p, lambda v: v.update(dump_size=v['dump_size'] + 1)))
    damaged('bad-hash', lambda p: (p / 'database.dump').write_bytes(dump[:-1] + bytes([dump[-1] ^ 1])))
    damaged('bad-size', lambda p: (p / 'database.dump').write_bytes(dump + b'\x00'))
    damaged('extra-path', lambda p: (p / 'extra').write_bytes(b'rejected'))
    damaged('world-readable', lambda p: (p / 'database.dump').chmod(0o644))

    def symlink_dump(p):
        (p / 'database.dump').unlink()
        (p / 'database.dump').symlink_to(original / 'database.dump')
    damaged('symlink', symlink_dump)
    traversal_target = 'rehearsal_' + uuid4().hex
    rehearse('../' + original.name, traversal_target, success=False)
    assert not restore_exists(traversal_target)
    print('PASS: incomplete/corrupt/unknown manifest, hash/size, extra file, permissions, symlink and traversal rejected before CREATE', flush=True)

    def quarantined(label, modify, code):
        directory = label + '-' + uuid4().hex
        destination = STATE / directory
        shutil.copytree(original, destination)
        modify(destination)
        database = 'rehearsal_' + uuid4().hex
        rehearse(directory, database, success=False, code=code)
        assert restore_exists(database), 'Failed restore destination was silently removed'
        rehearse(directory, database, success=False, code='TARGET_EXISTS')
        assert restore_exists(database), 'Repeated restore changed quarantined destination'

    def invalid_dump(p):
        value = b'PGDMP' + b'\x00' * 20
        (p / 'database.dump').write_bytes(value)
        alter_manifest(p, lambda v: v.update(dump_size=len(value), dump_sha256=hashlib.sha256(value).hexdigest()))

    quarantined('restore-failure', invalid_dump, 'RESTORE_FAILED')
    quarantined('verification-failure', lambda p: alter_manifest(p, lambda v: v['tables'][0].update(sha256='0' * 64)), 'RESTORE_VERIFY_FAILED')
    print('PASS: restore and inventory verification failures kept new DBs quarantined; retries refused overwrite', flush=True)

    remote = str(uuid4())
    pg(f"INSERT INTO support_messages(id,conversation_id,sender_account_id,sender_kind,text,created_at,telegram_only) VALUES('{remote}','{conversation}','{customer}','customer','synthetic remote-only media',now(),true)", 'backup_source')
    incomplete = 'incomplete-' + uuid4().hex
    backup(incomplete, success=False, code='BACKUP_INCOMPLETE')
    assert not (STATE / incomplete).exists()
    pg(f"DELETE FROM support_messages WHERE id='{remote}'", 'backup_source')
    audit = json.loads(pg(f"SELECT coalesce(json_agg(json_build_object('action',action,'reason',reason)), '[]'::json) FROM audit_events WHERE operator_account_id='{operator}' AND action LIKE 'backup.%'", 'backup_source'))
    operations = {}
    for event in audit:
        match = re.fullmatch(r'operation_id=([0-9a-f-]{36});code=([A-Z_]+)', event['reason'])
        assert match, 'Audit exposed noncanonical operation data'
        operations.setdefault(match[1], []).append((event['action'], match[2]))
    create_events = operations[str(manifest['operation_id'])]
    assert set(create_events) == {('backup.create.started', 'STARTED'), ('backup.create.succeeded', 'SUCCEEDED')}
    for events in operations.values():
        successes = [action for action, code in events if code == 'SUCCEEDED']
        failures = [action for action, code in events if action.endswith('.failed')]
        assert not (successes and failures), 'Failed operation has success audit'
        for action in successes:
            assert (action.replace('.succeeded', '.started'), 'STARTED') in events
    for code in ('BACKUP_INCOMPLETE', 'RESTORE_FAILED', 'RESTORE_VERIFY_FAILED', 'TARGET_EXISTS', 'OPERATOR_REQUIRED'):
        assert any(c == code and a.endswith('.failed') for events in operations.values() for a, c in events), 'Expected failure audit absent'
    assert pg(f"SELECT count(*) FROM audit_events WHERE operator_account_id='{foreign}' AND action='backup.create.failed' AND reason LIKE '%code=OPERATOR_REQUIRED'", 'backup_source') == '1'
    assert any(a == 'backup.rehearse.succeeded' for events in operations.values() for a, c in events)
    print('PASS: absent Telegram bytes fail full backup; correlated actor/start/success/failure audit verified; no runtime/providers started', flush=True)
    write('result.json', json.dumps({'project': runtime['project'], 'result': 'PASS', 'synthetic_only': True, 'real_pg_dump_restore': True}))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=('up', 'check', 'down'))
    action = parser.parse_args().action
    {'up': up, 'check': check, 'down': down}[action]()
