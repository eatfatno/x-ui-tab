#!/usr/bin/env python3
"""Run the real shell installer and Docker bundle with a local GitHub download fixture.

No real GitHub release is required. Only the three expected download URLs are
substituted; extraction, hashes, host installation, image load and startup are real.
Use the project's isolated Docker daemon and run as root in WSL.
"""
import hashlib
import json
import os
from pathlib import Path
import runpy
import subprocess
import sys
import uuid

ROOT = Path(__file__).resolve().parents[1]
NAME = 'x-ui-tab-3.9.0-1.0.1-linux-amd64'
BASE = ROOT / 'artifacts/fixtures' / ('online-install-' + uuid.uuid4().hex[:8])
STACK = BASE / 'deployment'
REPORT = ROOT / 'artifacts/reports/online-install-docker.json'


def main():
    if os.geteuid() != 0:
        raise RuntimeError('Run this isolated integration check as root')
    expected = 'unix://' + str(ROOT / 'cache/docker-engine/fs/docker.sock')
    if os.environ.get('DOCKER_HOST') != expected:
        raise RuntimeError('Use the isolated project Docker daemon')
    BASE.mkdir(parents=True)
    bridge = BASE / 'download-fixture'
    bridge.mkdir()
    archive = ROOT / 'artifacts/docker' / (NAME + '.tar.gz')
    metadata = {'tag_name': 'v1.0.1', 'draft': False, 'prerelease': False,
                'assets': [{'name': NAME + '.tar.gz'}, {'name': NAME + '.tar.gz.sha256'}]}
    (bridge / 'release.json').write_text(json.dumps(metadata))
    (bridge / 'sitecustomize.py').write_text('''import os
from pathlib import Path
import urllib.request
_original = urllib.request.urlopen
def _fixture(request, *args, **kwargs):
    url = request.full_url if hasattr(request, 'full_url') else str(request)
    name = os.environ['INSTALL_TEST_NAME']
    base = Path(os.environ['INSTALL_TEST_ROOT'])
    paths = {
        'https://api.github.com/repos/eatfatno/x-ui-tab/releases/tags/v1.0.1': base / 'release.json',
        'https://github.com/eatfatno/x-ui-tab/releases/download/v1.0.1/' + name + '.tar.gz': Path(os.environ['INSTALL_TEST_ARCHIVE']),
        'https://github.com/eatfatno/x-ui-tab/releases/download/v1.0.1/' + name + '.tar.gz.sha256': Path(os.environ['INSTALL_TEST_ARCHIVE'] + '.sha256'),
    }
    if url in paths:
        f = paths[url].open('rb')
        f.url = url
        return f
    return _original(request, *args, **kwargs)
urllib.request.urlopen = _fixture
''')
    env = os.environ | {'PYTHONPATH': str(bridge), 'INSTALL_TEST_NAME': NAME,
                        'INSTALL_TEST_ROOT': str(bridge), 'INSTALL_TEST_ARCHIVE': str(archive)}
    report = {'passed': False, 'fixture': str(BASE.relative_to(ROOT)),
              'download_source': 'local fixture simulating GitHub; live release not published', 'checks': []}
    args = ['bash', '-s', '--', '--no-install-deps',
            '--dir', str(STACK), '--bin-dir', str(BASE / 'bin'), '--bind', '127.0.0.1',
            '--port', '28083', '--panel-port', '22055', '--api-port', '63791', '--metrics-port', '21113',
            '--base-path', '/online/', '--project-name', 'x-ui-online-check']
    def cli(*args):
        return subprocess.run([sys.executable, str(STACK / 'x-ui'), *args], env=os.environ.copy(),
                              capture_output=True, text=True, timeout=180)
    try:
        script = (ROOT / 'scripts/install-online.sh').read_text()
        p = subprocess.run(args, input=script, env=env, capture_output=True, text=True, timeout=300)
        (BASE / 'installation.log').write_text(p.stdout + p.stderr)
        if p.returncode:
            raise RuntimeError('Installer failed; inspect ' + str(BASE / 'installation.log'))
        report['checks'].append('installer fed through stdin (curl | bash style) verifies and installs the real release archive')
        manifest = json.loads((STACK / 'bundle.json').read_text())
        ident = subprocess.check_output(['docker', 'image', 'inspect', '-f', '{{.Id}}', manifest['image']], text=True).strip()
        assert ident == manifest['image_id']
        assert (BASE / 'bin/x-ui').resolve() == STACK / 'x-ui'
        report['checks'].append('image identity and management entry match the release')
        Browser = runpy.run_path(str(ROOT / 'scripts/verify-runtime.py'))['Browser']
        browser = Browser('http://127.0.0.1:28083')
        credentials = json.loads((STACK / 'data/state/credentials.json').read_text())
        browser.obj('/online/login', credentials, {'X-CSRF-Token': browser.obj('/online/csrf-token')})
        assert browser.obj('/online/traffic-monitor/api/v1/clients') == []
        assert browser.obj('/online/traffic-monitor/api/v1/summary')['total'] == 0
        health = browser.obj('/online/traffic-monitor/api/v1/health')
        assert health['status'] == 'ok' and health['detail_days'] == 7 and health['summary_days'] == 35
        report['checks'].append('fresh credentials log in; empty monitoring data and 7/35 day policy are correct')
        before = (STACK / 'data/state/credentials.json').read_bytes()
        repeat = subprocess.run(args, input=script, env=env, capture_output=True, text=True, timeout=30)
        assert repeat.returncode != 0 and 'existing or partially initialized' in repeat.stderr
        assert (STACK / 'data/state/credentials.json').read_bytes() == before
        report['checks'].append('re-running installer refuses to overwrite existing data and credentials')
        report['passed'] = True
    finally:
        if (STACK / '.env').exists():
            cleaned = cli('remove', '--yes')
            report['test_container_removed'] = cleaned.returncode == 0
            if cleaned.returncode:
                report['passed'] = False
        REPORT.write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps(report, indent=2))


if __name__ == '__main__':
    main()
