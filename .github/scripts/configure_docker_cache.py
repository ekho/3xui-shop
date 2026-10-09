"""Configure only a disposable GitHub-hosted daemon, before its first container."""
import json
import os
from pathlib import Path
import stat
import subprocess
import sys
import tempfile


MIRROR = 'https://mirror.gcr.io'


def add_cache(config):
    if not isinstance(config, dict):
        raise ValueError('Docker daemon config must be an object')
    mirrors = config.get('registry-mirrors', [])
    if not isinstance(mirrors, list) or any(not isinstance(value, str) for value in mirrors):
        raise ValueError('Docker registry mirrors must be a list of strings')
    # shortcut: cache misses still use Docker Hub; add credentials only if misses become a blocker.
    return {**config, 'registry-mirrors': [MIRROR, *(value for value in mirrors if value.rstrip('/') != MIRROR)]}


def main():
    if sys.platform != 'linux' or os.environ.get('GITHUB_ACTIONS') != 'true' or os.environ.get('RUNNER_ENVIRONMENT') != 'github-hosted':
        raise RuntimeError('Docker cache setup requires a disposable GitHub-hosted Linux job')
    if subprocess.run(['docker', 'ps', '--all', '--quiet'], check=True, capture_output=True, text=True).stdout.strip():
        raise RuntimeError('Docker cache setup must precede all job containers')
    path = Path('/etc/docker/daemon.json')
    config = add_cache(json.loads(path.read_text()) if path.exists() else {})
    with tempfile.NamedTemporaryFile(mode='w', dir=path.parent, delete=False) as file:
        json.dump(config, file, indent=2)
        file.write('\n')
        candidate = Path(file.name)
    try:
        candidate.chmod(stat.S_IMODE(path.stat().st_mode) if path.exists() else 0o644)
        subprocess.run(['dockerd', '--validate', '--config-file', str(candidate)], check=True, capture_output=True)
        os.replace(candidate, path)
    finally:
        candidate.unlink(missing_ok=True)
    subprocess.run(['systemctl', 'restart', 'docker'], check=True, capture_output=True)
    info = subprocess.run(['docker', 'info', '--format', '{{json .RegistryConfig.Mirrors}}'], check=True, capture_output=True, text=True)
    if MIRROR not in [value.rstrip('/') for value in json.loads(info.stdout)]:
        raise RuntimeError('Docker daemon did not activate the registry mirror')
    print('Docker daemon registry mirror active: ' + MIRROR)


if __name__ == '__main__':
    main()
