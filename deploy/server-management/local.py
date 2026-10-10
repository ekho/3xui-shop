"""Isolated server-management panels, pinned provider and private acceptance helpers."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import re
import secrets
import time

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('acceptance_helpers', ROOT / 'deploy/acceptance/local.py')
local = importlib.util.module_from_spec(spec)
spec.loader.exec_module(local)
STATE = Path(os.environ.get('SERVER_MANAGEMENT_NATIVE_STATE', ROOT / '.superpowers/acceptance/server-management-native')).resolve()
local.STATE = STATE
METADATA = json.loads((STATE / 'runtime.json').read_text()) if (STATE / 'runtime.json').exists() else {}
PROJECT = METADATA.get('project', 'cabinet-server-management')
assert re.fullmatch(r'cabinet-[a-z0-9-]+', PROJECT), 'invalid owned project'
assert not METADATA or METADATA.get('purpose') == 'server-management-acceptance', 'invalid fixture purpose'


def compose(*args):
    return local.command(['docker', 'compose', '--project-name', PROJECT,
                          '--env-file', str(STATE / 'public.env'), '-f',
                          'deploy/server-management/compose.yml', *args])


def prepare():
    STATE.mkdir(parents=True, mode=0o700, exist_ok=True)
    STATE.chmod(0o700)
    if not METADATA:
        local.write('runtime.json', json.dumps({'purpose': 'server-management-acceptance', 'project': PROJECT}))
    for name in ('panel-db', 'panel-second-db'):
        (STATE / name).mkdir(mode=0o700, exist_ok=True)
    if not (STATE / 'panel-password').exists():
        local.write('panel-password', secrets.token_urlsafe(32))
    if not (STATE / 'cert.pem').exists():
        local.command(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes',
                       '-days', '30', '-subj', '/CN=localhost', '-addext',
                       'subjectAltName=DNS:localhost,IP:127.0.0.1', '-keyout',
                       str(STATE / 'key.pem'), '-out', str(STATE / 'cert.pem')])
        (STATE / 'key.pem').chmod(0o600)
    # Compose also interpolates unused secrets in the extended source file.
    local.write('public.env', '\n'.join((f'LOCAL_STATE_DIR={STATE}',
                f'APP_RUNTIME_UID={os.getuid()}', f'APP_RUNTIME_GID={os.getgid()}',
                f'PANEL_PASSWORD_FILE={STATE / "panel-password"}', 'SMTP_AUTH_FILE=/dev/null')) + '\n')


def panel(port, initialize=False):
    assert port in (61444, 61449), 'only owned server-management panels'
    origin = f'https://localhost:{port}'
    opener = local.session()

    def call(path, body=None, csrf=None):
        status, _, raw = local.request(opener, origin + '/' + path, body,
                                      {'X-CSRF-Token': csrf} if csrf else {})
        reply = json.loads(raw)
        assert status == 200 and reply.get('success') is True, 'owned panel request failed'
        return reply.get('obj')

    csrf = call('csrf-token')
    marker = STATE / f'initialized-{port}'
    password = (STATE / 'panel-password').read_text().strip()
    if initialize and not marker.exists():
        call('login', {'username': 'admin', 'password': 'admin'}, csrf)
        call('panel/api/setting/updateUser', {'oldUsername': 'admin', 'oldPassword': 'admin',
             'newUsername': 'local-operator', 'newPassword': password}, csrf)
        local.write(marker.name, 'yes')
    call('login', {'username': 'local-operator', 'password': password}, csrf)
    if initialize:
        if not any(row.get('tag') == 'local-regular-vless' for row in call('panel/api/inbounds/list')):
            call('panel/api/inbounds/add', {'enable': True, 'remark': 'Server management fixture',
                 'listen': '0.0.0.0', 'port': 24443, 'protocol': 'vless', 'tag': 'local-regular-vless',
                 'settings': {'clients': [], 'decryption': 'none', 'fallbacks': []},
                 'streamSettings': {'network': 'tcp', 'security': 'none'},
                 'sniffing': {'enabled': False}}, csrf)
        settings = call('panel/api/setting/all', {}, csrf)
        settings.update(subEnable=True, subURI=origin + '/sub/')
        call('panel/api/setting/update', settings, csrf)
    rows = call('panel/api/inbounds/list')
    assert any(row.get('tag') == 'local-regular-vless' for row in rows), 'owned inbound absent'
    return len(rows)


def up():
    prepare()
    compose('config', '--quiet')
    compose('build', 'gateway')
    compose('up', '--pull', 'missing', '-d')
    for port in (61444, 61449):
        deadline = time.monotonic() + 30
        while True:
            try:
                local.request(local.session(), f'https://localhost:{port}/csrf-token')
                break
            except (OSError, ValueError):
                assert time.monotonic() < deadline, 'owned panel TLS readiness timed out'
                time.sleep(.2)
        panel(port, initialize=True)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=('up', 'check', 'down'))
    action = parser.parse_args().action
    if action == 'up':
        up()
    elif action == 'down':
        compose('down')
    else:
        compose('config', '--quiet')
        assert panel(61444) > 0 and panel(61449) > 0
    print('PASS: isolated server-management panel fixture ' + action)
