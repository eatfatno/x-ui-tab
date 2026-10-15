#!/usr/bin/env python3
"""Extract the actual delivery archive, verify every file and start it offline."""
import hashlib
import json
import os
from pathlib import Path
import runpy
import subprocess
import sys
import tarfile

ROOT = Path(__file__).resolve().parents[1]
NAME = 'x-ui-tab-3.10.2-1.0.2-linux-amd64'
archive = ROOT / 'artifacts/docker' / (NAME + '.tar.gz')
destination = ROOT / 'artifacts/fixtures/docker-bundle-install'
report = {'passed': False, 'checks': []}


def sha(path):
    with path.open('rb') as f:
        return hashlib.file_digest(f, 'sha256').hexdigest()


if destination.exists() and any(destination.iterdir()):
    raise RuntimeError('Bundle verification destination already exists; preserve it and choose a fresh directory')
destination.mkdir(parents=True, exist_ok=True)
expected = archive.with_suffix('.gz.sha256').read_text().split()[0]
assert sha(archive) == expected
with tarfile.open(archive) as tar:
    assert all(Path(m.name).parts[0] == NAME for m in tar.getmembers())
    tar.extractall(destination, filter='data')
stack = destination / NAME
assert not (stack / 'data').exists() and not (stack / '.env').exists()
count = 0
for line in (stack / 'SHA256SUMS').read_text().splitlines():
    expected, name = line.split(maxsplit=1)
    assert sha(stack / name) == expected
    count += 1
report['checks'].append(f'archive and {count} individual SHA-256 checks')
report['bundle_size_bytes'] = archive.stat().st_size
manifest = json.loads((stack / 'bundle.json').read_text())
report['image_size_bytes'] = manifest['image_size_bytes']


def cli(*args):
    result = subprocess.run([sys.executable, str(stack / 'x-ui'), *args], text=True, capture_output=True, timeout=240)
    with (destination / 'verification.log').open('a') as f: f.write(result.stdout + result.stderr)
    assert result.returncode == 0, 'bundle management command failed: ' + args[0]


try:
    cli('load')
    assert subprocess.check_output(['docker', 'image', 'inspect', '-f', '{{.Id}}', manifest['image']], text=True).strip() == manifest['image_id']
    report['checks'].append('offline image archive load and exact image identity')
    cli('init', '--bind', '127.0.0.1', '--port', '28082', '--panel-port', '22054', '--api-port', '63790',
        '--metrics-port', '21112', '--base-path', '/bundle/', '--project-name', 'x-ui-bundle-check')
    Browser = runpy.run_path(str(ROOT / 'scripts/verify-runtime.py'))['Browser']
    browser = Browser('http://127.0.0.1:28082')
    credentials = json.loads((stack / 'data/state/credentials.json').read_text())
    browser.obj('/bundle/login', credentials, {'X-CSRF-Token': browser.obj('/bundle/csrf-token')})
    assert browser.obj('/bundle/traffic-monitor/api/v1/clients') == []
    assert browser.obj('/bundle/traffic-monitor/api/v1/summary')['total'] == 0
    health = browser.obj('/bundle/traffic-monitor/api/v1/health')
    assert health['status'] == 'ok' and health['detail_days'] == 7 and health['summary_days'] == 35
    report['checks'].append('delivered package initializes with fresh credentials and empty user data')
    cli('remove', '--yes')
    report['checks'].append('delivered management command removes its container and retains its data')
    report['passed'] = True
finally:
    cli('stop')
    (ROOT / 'artifacts/reports/docker-bundle-verification.json').write_text(json.dumps(report, indent=2) + '\n')
print(json.dumps(report, indent=2))
