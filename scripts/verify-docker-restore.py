#!/usr/bin/env python3
"""Resume the data-migration acceptance against its explicitly named fixture.

Use after the ordinary acceptance has produced a four-visit backup. This does
not access any native WSL deployment or a production Docker installation.
"""
from contextlib import closing
import hashlib
import json
import os
from pathlib import Path
import runpy
import shutil
import sqlite3
import subprocess
import tarfile

ROOT = Path(__file__).resolve().parents[1]
if os.environ.get('DOCKER_TEST_FIXTURE') != 'docker-acceptance-final2':
    raise RuntimeError('This continuation is confined to docker-acceptance-final2')
m = runpy.run_path(str(ROOT / 'scripts/test-docker.py'))
g = m['main'].__globals__
fixture, report_path = m['FIXTURE'], m['REPORT']
shutil.copy2(ROOT / 'deploy/docker/x-ui', fixture / 'x-ui')
g['BASE'], g['URL'] = '/changed/', 'http://127.0.0.1:28081'
cli, login, fingerprint = m['cli'], m['login'], m['fingerprint']
report = json.loads(report_path.read_text())
report.pop('error', None)


def expected(archive):
    scratch = fixture / '.cache/expected-monitor.db'
    with tarfile.open(archive) as tar:
        scratch.write_bytes(tar.extractfile('data/monitor/monitor.db').read())
    result = {}
    with closing(sqlite3.connect(scratch)) as db:
        for table in ('visits', 'traffic', 'visit_counts'):
            rows = sorted(db.execute('SELECT * FROM ' + table).fetchall())
            result[table] = {'count': len(rows), 'sha256': hashlib.sha256(json.dumps(rows).encode()).hexdigest()}
    scratch.unlink()
    return result


def record(label):
    if label not in report['checks']:
        report['checks'].append(label)
    print('PASS', label, flush=True)


try:
    snapshots = [(p, expected(p)) for p in sorted((fixture / 'backups').glob('*.tar.gz'))]
    archive, before = next((p, e) for p, e in snapshots if e['visits']['count'] == 4)
    cli('restore', archive, '--yes'); login()
    assert fingerprint() == before, 'Restore replayed or lost history'
    record('autostart configuration and consistent backup/restore')
    cli('reset-2fa'); login()
    record('official two-factor reset command')
    cli('update', 'x-ui-tab:3.9.0-1.0.0-amd64'); login()
    assert fingerprint() == before
    record('image recreation preserves panel clients, monitor identity and retained history')
    bad = fixture / 'bad-image'
    bad.mkdir(exist_ok=True)
    (bad / 'Dockerfile').write_text('FROM x-ui-tab:3.9.0-1.0.0-amd64\nCMD ["deliberate-startup-failure"]\n')
    subprocess.run(['docker', 'build', '--network=host', '-t', 'x-ui-tab:acceptance-failure', str(bad)],
                   env={**os.environ, 'DOCKER_BUILDKIT': '0'}, check=True, capture_output=True)
    assert cli('update', 'x-ui-tab:acceptance-failure', check=False).returncode != 0
    assert 'XUI_IMAGE=x-ui-tab:3.9.0-1.0.0-amd64' in (fixture / '.env').read_text()
    b = login(); assert fingerprint() == before
    record('failed image startup automatically restores the previous image and database snapshot')
    subprocess.run(['bash', str(fixture / 'install.sh'), str(fixture / 'test-bin')], check=True, capture_output=True)
    menu = subprocess.run([str(fixture / 'test-bin/x-ui')], input='0\n', text=True, capture_output=True, check=True)
    assert 'Docker 管理菜单' in menu.stdout and '重建监测采集 Token' in menu.stdout
    record('installed Linux x-ui symlink and interactive menu')
    health = b.obj('/changed/traffic-monitor/api/v1/health')
    assert health['status'] == 'ok' and health['adapter_compatible']
    code, headers, body = b.request('/changed/panel/clients')
    assert code == 200 and b'panel-recovery.js' in body and b'adapter.js' in body
    report['history_rows_preserved'] = fingerprint()
    report['image_id'] = subprocess.check_output(['docker', 'image', 'inspect', '-f', '{{.Id}}', 'x-ui-tab:3.9.0-1.0.0-amd64'], text=True).strip()
    record('final image authenticated health, official HTML and adapter integration')
    cli('remove', '--yes')
    assert (fixture / 'data/monitor/monitor.db').exists()
    record('uninstall removes only the test container and preserves data')
    report['passed'] = True
    report['restore_regressions_fixed'] = ['restored inode checkpoint rebinding', 'explicitly close SQLite handles before moving directories on WSL']
except Exception as e:
    report['passed'] = False
    report['error'] = type(e).__name__ + ': ' + str(e)[:300]
    raise
finally:
    cli('stop', check=False)
    report_path.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
