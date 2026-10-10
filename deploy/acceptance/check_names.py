"""Reject roadmap labels in application paths and authored source."""
from pathlib import Path
import re
import subprocess

ROOT = Path(__file__).resolve().parents[2]
label = re.compile(r'(?<![A-Za-z0-9])(?:[sSсС]\d{2}|Test[SС]\d{2})(?!\d)')
path_label = re.compile(r'(?:^|[/_.-])[sSсС]\d{2}(?:[/_.-]|$)')
opaque = re.compile(r'\s*"[A-Za-z0-9+/=]{70,}",?\s*')
files = subprocess.check_output(
    ['git', 'ls-files', '--cached', '--others', '--exclude-standard', '-z'], cwd=ROOT
).decode().split('\0')
failures = []
for name in sorted(set(files)):
    path = ROOT / name
    if not name or not path.is_file():
        continue
    if not name.startswith('docs/') and path_label.search(name):
        failures.append(name + ': scenario in path')
    if path.suffix == '.md' or path.name in ('go.sum', 'package-lock.json', 'uv.lock'):
        continue
    try:
        lines = path.read_text().splitlines()
    except UnicodeError:
        continue
    for number, line in enumerate(lines, 1):
        if not opaque.fullmatch(line) and label.search(line):
            failures.append(f'{name}:{number}: scenario in source')
if failures:
    raise SystemExit('\n'.join(failures))
print('PASS: application paths and authored source use semantic names')
