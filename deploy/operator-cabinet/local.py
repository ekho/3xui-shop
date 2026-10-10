"""Private bridge for the owned Support/Operator cabinet Docker acceptance stack.

JSON comes from stdin. Only bounded, redacted JSON goes to stdout.
"""
import importlib.util
import contextlib
from hashlib import sha256
import io
import json
import os
from pathlib import Path
import re
import sys
from uuid import UUID

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('acceptance_local', ROOT / 'deploy/acceptance/local.py')
local = importlib.util.module_from_spec(spec)
spec.loader.exec_module(local)
STATE = local.acceptance_state('operator-cabinet')


def own_uuid(value):
    return str(UUID(value))


def query(statement, **ids):
    prefix = ''
    for name, value in ids.items():
        checked = value if name.endswith('_pattern') else own_uuid(value)
        if name.endswith('_pattern') and checked not in (
            local.fixture_pattern('operator-cabinet'), 'operator-cabinet-%@example.test'):
            raise ValueError('fixture pattern outside owned purpose')
        prefix += "\\set %s '%s'\n" % (name, checked)
    return local.compose('exec', '-T', 'postgres', 'psql', '-U', local.PG_USER,
                         '-d', local.database(), '-qAt', '-v', 'ON_ERROR_STOP=1',
                         stdin=(prefix + statement).encode()).decode().strip()


def role(action, account):
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


def native(operation):
    raw = query("""SELECT json_build_object('status',o.status,'target',o.target,
      'grant',(SELECT status FROM trial_grants WHERE operation_id=o.id),
      'grants',(SELECT count(*) FROM trial_grants WHERE operation_id=o.id),
      'jobs',(SELECT count(*) FROM river_job WHERE kind='trial_provision'
        AND args->>'operation_id'=o.id::text))
      FROM trial_operations o WHERE o.id=:'operation'::uuid;""", operation=operation)
    if not raw:
        raise RuntimeError('operation missing')
    row = json.loads(raw)
    row['target_digest'] = sha256(json.dumps(row['target'], sort_keys=True).encode()).hexdigest()
    if row['status'] in ('applied', 'needs_review') and row['target']:
        panel = local.panel_readback(row['target'])
        row['panel_identity'] = panel['uuid'] == row['target']['vpn_id'] and panel['subId'] == row['target']['sub_id']
        row['panel_digest'] = sha256(json.dumps(panel, sort_keys=True).encode()).hexdigest()
    row.pop('target')
    return row


def support_snapshot(account):
    raw = query("""SELECT json_build_object('status',c.status,'banned',c.support_banned,
      'customer_received',c.customer_received_sequence,'operator_received',c.operator_received_sequence,
      'messages',(SELECT count(*) FROM support_messages m WHERE m.conversation_id=c.id),
      'files',(SELECT count(*) FROM support_messages m WHERE m.conversation_id=c.id AND m.attachment_bytes IS NOT NULL),
      'bytes',(SELECT coalesce(sum(octet_length(m.attachment_bytes)),0) FROM support_messages m WHERE m.conversation_id=c.id),
      'file_hashes',(SELECT json_agg(md5(m.attachment_bytes) ORDER BY m.sequence)
        FROM support_messages m WHERE m.conversation_id=c.id AND m.attachment_bytes IS NOT NULL))
      FROM support_conversations c WHERE c.account_id=:'account'::uuid;""", account=account)
    return json.loads(raw) if raw else None


def identity_snapshot(account):
    raw = query("""SELECT json_build_object('kind',a.kind,'email_null',a.email_key IS NULL,
      'password_null',a.password_hash IS NULL,'verified_null',a.verified_at IS NULL,
      'telegram_present',a.telegram_id IS NOT NULL,
      'role',EXISTS(SELECT 1 FROM operator_accounts x WHERE x.account_id=a.id),
      'web_decisions',(SELECT count(*) FROM trial_requests t WHERE t.account_id=a.id AND t.operator_account_id IS NOT NULL),
      'grants',(SELECT count(*) FROM trial_grants g JOIN trial_operations o ON o.id=g.operation_id WHERE o.account_id=a.id))
      FROM accounts a WHERE a.id=:'account'::uuid;""", account=account)
    return json.loads(raw) if raw else None


def restricted_state(account, replacement=None):
    before = query("""SELECT json_build_object('restricted',a.restricted,
      'dedicated',a.kind='web' AND (a.email_key LIKE :'fixture_pattern'
        OR a.email_key LIKE :'new_fixture_pattern')
        AND EXISTS(SELECT 1 FROM operator_accounts x WHERE x.account_id=a.id))
      FROM accounts a WHERE a.id=:'account'::uuid;""", account=account,
      fixture_pattern=local.fixture_pattern('operator-cabinet'),
      new_fixture_pattern='operator-cabinet-%@example.test')
    state = json.loads(before) if before else None
    if state is None or not state['dedicated']:
        raise RuntimeError('restricted fixture operator guard failed')
    if replacement is not None:
        if type(replacement) is not bool:
            raise ValueError('restricted fixture value invalid')
        flag = 'true' if replacement else 'false'
        query("UPDATE accounts SET restricted=" + flag +
              " WHERE id=:'account'::uuid;", account=account)
    return {'restricted': replacement if replacement is not None else state['restricted'],
            'operator_role': state['dedicated']}


def state_digest(account):
    raw = query("""SELECT json_build_object(
      'account',md5(to_jsonb(a)::text),
      'role',(SELECT md5(coalesce(jsonb_agg(to_jsonb(x) ORDER BY x.account_id)::text,'[]'))
        FROM operator_accounts x WHERE x.account_id=a.id),
      'conversation',(SELECT md5(coalesce(jsonb_agg(to_jsonb(c) ORDER BY c.id)::text,'[]'))
        FROM support_conversations c WHERE c.account_id=a.id),
      'messages',(SELECT md5(coalesce(jsonb_agg(to_jsonb(m) ORDER BY m.sequence)::text,'[]'))
        FROM support_messages m JOIN support_conversations c ON c.id=m.conversation_id WHERE c.account_id=a.id),
      'trials',(SELECT md5(coalesce(jsonb_agg(to_jsonb(t) ORDER BY t.sequence)::text,'[]'))
        FROM trial_requests t WHERE t.account_id=a.id),
      'audit',(SELECT md5(coalesce(jsonb_agg(to_jsonb(e) ORDER BY e.created_at,e.id)::text,'[]'))
        FROM audit_events e WHERE e.account_id=a.id),
      'operations',(SELECT md5(coalesce(jsonb_agg(to_jsonb(o) ORDER BY o.id)::text,'[]'))
        FROM trial_operations o WHERE o.account_id=a.id),
      'grants',(SELECT md5(coalesce(jsonb_agg(to_jsonb(g) ORDER BY g.account_id)::text,'[]'))
        FROM trial_grants g WHERE g.account_id=a.id))
      FROM accounts a WHERE a.id=:'account'::uuid;""", account=account)
    return json.loads(raw) if raw else None


def review_off():
    query("DROP TRIGGER IF EXISTS local_operator_block_apply ON trial_operations; "
          "DROP FUNCTION IF EXISTS local_operator_block_apply();")


def review_on(account):
    guard = json.loads(query("""SELECT json_build_object(
      'provisioning',(SELECT count(*) FROM trial_operations WHERE status='provisioning'),
      'running',(SELECT count(*) FROM river_job WHERE kind='trial_provision' AND state='running'),
      'prior',(SELECT count(*) FROM trial_requests WHERE account_id=:'account'::uuid),
      'valid',(SELECT kind='web' AND verified_at IS NOT NULL AND NOT restricted FROM accounts WHERE id=:'account'::uuid),
      'trigger',(SELECT count(*) FROM pg_trigger WHERE tgname='local_operator_block_apply'),
      'function',to_regprocedure('local_operator_block_apply()') IS NOT NULL);""", account=account))
    if guard != {'provisioning': 0, 'running': 0, 'prior': 0, 'valid': True, 'trigger': 0, 'function': False}:
        raise RuntimeError('controlled review preflight failed')
    try:
        query("""CREATE FUNCTION local_operator_block_apply() RETURNS trigger LANGUAGE plpgsql AS $local_operator$
          BEGIN RAISE EXCEPTION 'local operator controlled apply failure'; END $local_operator$;
          CREATE TRIGGER local_operator_block_apply BEFORE UPDATE ON trial_operations
          FOR EACH ROW WHEN (NEW.account_id=:'account'::uuid AND NEW.status='applied')
          EXECUTE FUNCTION local_operator_block_apply();""", account=account)
    except Exception:
        review_off()
        raise
    return {'installed': True}


def restore():
    vpn_path = local.STATE / 'vpn.json'
    prior = vpn_path.read_bytes()
    database_path = local.STATE / 'database-url'
    database_before = database_path.read_bytes()
    connected = local.vpn_connected()
    config = local.ENV.read_text()
    current_transport = transport()
    if not (current_transport['no_telegram_operators'] and current_transport['telegram_disabled']
            and current_transport['adapter_stopped']):
        raise RuntimeError('native web-only precondition not met')
    complete = False
    def stop_reconcile():
        try:
            local.compose('stop', 'reconcile')
        except Exception:
            # This service belongs to the same disposable cabinet project.
            local.compose('kill', '-s', 'SIGKILL', 'reconcile')
        if local.compose('ps', '-q', 'reconcile').decode().strip():
            raise RuntimeError('owned reconcile still running')
    try:
        with contextlib.redirect_stdout(io.StringIO()):
            local.restore()
        complete = True
    finally:
        cleanup_errors = []
        reconcile_stopped = False
        try:
            stop_reconcile()
            reconcile_stopped = True
        except Exception:
            cleanup_errors.append('owned reconcile')
        if not complete:
            try:
                if not reconcile_stopped:
                    raise RuntimeError('owned reconcile not stopped')
                database_path.write_bytes(database_before)
                database_path.chmod(0o600)
                # The reused Web trial restore may have stopped between installing
                # its local fault fixture and removing it.
                query('DROP TRIGGER IF EXISTS local_pause_apply ON trial_operations; '
                      'DROP FUNCTION IF EXISTS local_pause_apply();')
            except Exception:
                cleanup_errors.append('database pointer or local pause fixture')
        try:
            local.write('public.env', config)
        except Exception:
            cleanup_errors.append('native configuration')
        try:
            local.compose('up', '--no-build', '--pull', 'never', '--no-deps', '--force-recreate', '-d', 'backend')
            local.compose('up', '--no-build', '--pull', 'never', '--no-deps', '--force-recreate', '-d', 'gateway')
            local.wait_until(local.ready)
        except Exception:
            cleanup_errors.append('cabinet backend/gateway')
        try:
            vpn_path.write_bytes(prior)
            vpn_path.chmod(0o600)
            if connected:
                local.compose('--profile', 'vpn', 'up', '--no-build', '--pull', 'never',
                              '--force-recreate', '-d', 'vpn-client')
                local.wait_until(local.vpn_connected)
        except Exception:
            cleanup_errors.append('owned VPN')
        try:
            final_transport = transport()
            if not (final_transport['no_telegram_operators'] and final_transport['telegram_disabled']
                    and final_transport['adapter_stopped']):
                cleanup_errors.append('native Telegram configuration')
        except Exception:
            cleanup_errors.append('native Telegram configuration check')
        if cleanup_errors:
            # Leave the original healthy pointer/config active when finalization failed.
            try:
                stop_reconcile()
                database_path.write_bytes(database_before)
                database_path.chmod(0o600)
                local.write('public.env', config)
                local.compose('up', '--no-build', '--pull', 'never', '--no-deps', '--force-recreate', '-d', 'backend')
                local.compose('up', '--no-build', '--pull', 'never', '--no-deps', '--force-recreate', '-d', 'gateway')
                local.wait_until(local.ready)
                vpn_path.write_bytes(prior)
                vpn_path.chmod(0o600)
                if connected:
                    local.compose('--profile', 'vpn', 'up', '--no-build', '--pull', 'never',
                                  '--force-recreate', '-d', 'vpn-client')
                    local.wait_until(local.vpn_connected)
            except Exception:
                cleanup_errors.append('original database recovery')
            raise RuntimeError('restore cleanup failed: ' + ', '.join(cleanup_errors))
        stop_reconcile()
    final = transport()
    return {'restored': True, 'prior_vpn_connected': local.vpn_connected() == connected,
            'prior_vpn_config_equal': vpn_path.read_bytes() == prior,
            'no_telegram_operators': final['no_telegram_operators'],
            'telegram_disabled': final['telegram_disabled'], 'adapter_stopped': final['adapter_stopped']}


def transport():
    config = dict(line.split('=', 1) for line in local.ENV.read_text().splitlines() if '=' in line)
    backend = local.compose('ps', '-q', 'backend').decode().strip()
    running = set()
    if backend:
        result = local.command(['docker', 'inspect', '--format',
                                '{{range .Config.Env}}{{if eq . "BOT_OPERATOR_IDS="}}operators-empty {{end}}'
                                '{{if eq . "TELEGRAM_ENABLED=false"}}telegram-disabled {{end}}{{end}}',
                                backend]).decode().strip()
        running = set(result.split())
    bot = local.command(['docker', 'ps', '-q', '--filter', 'label=com.docker.compose.project=' + local.PROJECT,
                         '--filter', 'label=com.docker.compose.service=bot']).decode().strip()
    return {'no_telegram_operators': config.get('BOT_OPERATOR_IDS') is not None
            and config['BOT_OPERATOR_IDS'].strip() == '' and 'operators-empty' in running,
            'telegram_disabled': config.get('TELEGRAM_ENABLED') == 'false' and 'telegram-disabled' in running,
            'adapter_stopped': bot == ''}


def genuine_tg_id():
    lines = (ROOT / '.env').read_text().splitlines()
    values = [line.split('=', 1)[1].strip().strip('\"\'') for line in lines if line.startswith('ADMIN_TG_ID=')]
    if len(values) != 1 or not re.fullmatch(r'[1-9][0-9]{0,18}', values[0]) or int(values[0]) > 9223372036854775807:
        raise RuntimeError('genuine Telegram ID unavailable')
    return values[0]


def run(data):
    action = data['action']
    if action in ('grant', 'revoke'):
        return {'role': role(action, data['account'])}
    if action == 'native':
        return native(data['operation'])
    if action == 'support':
        return support_snapshot(data['account'])
    if action == 'identity':
        return identity_snapshot(data['account'])
    if action == 'restricted_get':
        return restricted_state(data['account'])
    if action == 'restricted_set':
        return restricted_state(data['account'], data['restricted'])
    if action == 'digest':
        return state_digest(data['account'])
    if action == 'restore':
        return restore()
    if action == 'review_on':
        return review_on(data['account'])
    if action == 'review_off':
        review_off()
        return {'removed': True}
    if action == 'tg_available':
        value = genuine_tg_id()
        # Positive int64 from the owner's private .env; stdin only, no argv value.
        statement = "\\set tg_id '%s'\nSELECT count(*) FROM accounts WHERE telegram_id=:'tg_id'::bigint;" % value
        return {'available': local.compose('exec', '-T', 'postgres', 'psql', '-U', local.PG_USER,
                '-d', local.database(), '-qAt', '-v', 'ON_ERROR_STOP=1', stdin=statement.encode()).decode().strip() == '0'}
    if action == 'vpn':
        return {'connected': local.vpn_connected()}
    if action == 'transport':
        return transport()
    raise ValueError('unknown action')


if __name__ == '__main__':
    try:
        print(json.dumps(run(json.load(sys.stdin))))
    except Exception:
        print('Operator cabinet local bridge failed', file=sys.stderr)
        raise SystemExit(1)
