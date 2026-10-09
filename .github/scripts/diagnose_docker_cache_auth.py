"""One-shot hosted-runner comparison; never emit credentials or provider bodies."""
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import urllib.error
import urllib.parse
import urllib.request

MIRROR = 'https://mirror.gcr.io'
DIGEST = 'sha256:6f81e8915c60b065a524e6967e0ad1c639ba6efa84d669f823683ea04d9150ee'
IMAGE = 'docker.io/library/redis@' + DIGEST
HUB_KEYS = ('docker.io', 'registry-1.docker.io', 'https://index.docker.io/v1/')


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def request(label, path, token=None):
    headers = {'Accept': 'application/vnd.oci.image.index.v1+json,application/vnd.docker.distribution.manifest.list.v2+json'}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    try:
        response = urllib.request.build_opener(NoRedirect).open(
            urllib.request.Request(MIRROR + path, headers=headers), timeout=30)
    except urllib.error.HTTPError as error:
        response = error
    print('Mirror protocol:', label, 'status:', response.status)
    return response


def main():
    if sys.platform != 'linux' or os.environ.get('GITHUB_ACTIONS') != 'true' or os.environ.get('RUNNER_ENVIRONMENT') != 'github-hosted':
        raise RuntimeError('Diagnosis requires a disposable GitHub-hosted Linux job')
    config = Path(os.environ.get('DOCKER_CONFIG', Path.home() / '.docker')) / 'config.json'
    metadata = json.loads(config.read_text()) if config.exists() else {}
    print('Client auth metadata: Hub auth keys:', [key for key in HUB_KEYS if key in metadata.get('auths', {})],
          'Hub helper keys:', [key for key in HUB_KEYS if key in metadata.get('credHelpers', {})],
          'global credential store key present:', 'credsStore' in metadata)
    del metadata
    with request('ping', '/v2/') as response:
        challenge = response.headers.get('WWW-Authenticate', '')
        fields = dict(re.findall(r'(\w+)="([^"]*)"', challenge))
        print('Mirror challenge: bearer:', challenge.lower().startswith('bearer '),
              'expected realm:', fields.get('realm') == MIRROR + '/v2/token',
              'service present:', 'service' in fields)
    manifest = '/v2/library/redis/manifests/' + DIGEST
    with request('anonymous manifest', manifest):
        pass
    assert fields.get('realm') == MIRROR + '/v2/token'
    assert fields.get('service', '') in ('', 'mirror.gcr.io')
    query = [('scope', 'repository:library/redis:pull'), ('client_id', 'docker')]
    if 'service' in fields:
        query.append(('service', fields['service']))
    with request('anonymous token', '/v2/token?' + urllib.parse.urlencode(query)) as response:
        assert response.status == 200
        body = response.read(65537)
        assert len(body) <= 65536
        data = json.loads(body)
        token = data.get('token') or data.get('access_token')
        assert isinstance(token, str) and token and '\n' not in token and '\r' not in token
    with request('bearer manifest', manifest, token):
        pass
    del token, body, data
    with tempfile.TemporaryDirectory(prefix='anonymous-docker-', dir=os.environ['RUNNER_TEMP']) as empty:
        for label, args in (('default', ['docker']), ('empty', ['docker', '--config', empty])):
            try:
                result = subprocess.run([*args, 'pull', IMAGE], capture_output=True, text=True, timeout=90)
                output = result.stdout + result.stderr
                print('Canonical pull:', label, 'exit:', result.returncode,
                      'categories:', [value for value in ('unauthorized', 'timeout', '429', 'unauthenticated pull rate limit') if value in output.lower()],
                      'upstream host present:', 'auth.docker.io' in output or 'registry-1.docker.io' in output)
            except subprocess.TimeoutExpired:
                print('Canonical pull:', label, 'category: process-timeout')


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        print('Diagnosis stopped:', type(error).__name__)
        sys.exit(1)
