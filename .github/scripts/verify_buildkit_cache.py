"""Verify actual mirror requests without exposing raw build traces."""
import json
import os
import re
import subprocess
import tomllib
from urllib.parse import urlsplit


def mirror_manifest_requests(trace):
    count = 0
    for batch in trace.get('data', []):
        for span in batch.get('spans', []):
            if span.get('operationName') != 'remotes.docker.resolver.HTTPRequest':
                continue
            tags = {tag['key']: tag['value'] for tag in span.get('tags', [])}
            if (tags.get('server.address') != 'mirror.gcr.io'
                    or tags.get('http.request.method') != 'HEAD'
                    or tags.get('http.response.status_code') != 200):
                continue
            value = tags.get('url.full')
            if not isinstance(value, str):
                continue
            try:
                url = urlsplit(value)
                if (url.scheme == 'https' and url.hostname == 'mirror.gcr.io'
                        and url.port in (None, 443) and not url.username and not url.password
                        and url.path.startswith('/v2/') and '/manifests/' in url.path):
                    count += 1
            except ValueError:
                continue
    return count


def docker(arguments):
    return subprocess.run(['docker', *arguments], check=True, capture_output=True,
                          text=True, timeout=120).stdout


def main():
    builder = os.environ['BUILDX_BUILDER']
    config = tomllib.loads(docker(['exec', f'buildx_buildkit_{builder}0',
                                  'cat', '/etc/buildkit/buildkitd.toml']))
    mirrors = config.get('registry', {}).get('docker.io', {}).get('mirrors', [])
    if not isinstance(mirrors, list) or 'mirror.gcr.io' not in mirrors:
        raise RuntimeError('BuildKit container mirror config is missing')
    print('BuildKit container mirror config verified')
    records = docker(['buildx', 'history', 'ls', '--builder', builder,
                      '--filter', 'status=completed', '--format', '{{.Ref}}']).splitlines()
    for ref in records:
        if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9._/-]{0,255}', ref):
            raise RuntimeError('Unexpected BuildKit history identifier')
        # A pipe keeps history trace headless; raw JSON stays in process memory.
        data = docker(['buildx', 'history', 'trace', '--builder', builder, ref])
        try:
            count = mirror_manifest_requests(json.loads(data))
        except (ValueError, TypeError, AttributeError, KeyError):
            raise RuntimeError('Unexpected Buildx trace format; raw output withheld') from None
        if count:
            print(f'BuildKit HTTPS mirror manifest requests verified: {count}')
            return
    raise RuntimeError('No successful HTTPS mirror manifest requests in completed build traces')


if __name__ == '__main__':
    main()
