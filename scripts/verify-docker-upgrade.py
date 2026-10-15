#!/usr/bin/env python3
"""Real 3.9.0 -> 3.10.2 Docker migration and post-migration rollback.

Runs only against a disposable fixture. Both versioned images must already be
loaded into the selected project Docker engine. No external proxy is used.
"""
from contextlib import closing
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import sqlite3
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[1]
OLD = 'x-ui-tab:3.9.0-1.0.1-amd64'
NEW = 'x-ui-tab:3.10.2-1.0.2-amd64'
BAD = 'x-ui-tab:upgrade102-after-migration-failure'
FIXTURE = ROOT / 'artifacts/fixtures/docker-acceptance-upgrade102'
REPORT = ROOT / 'artifacts/reports/docker-version-upgrade.json'


def main():
    spec = importlib.util.spec_from_file_location('docker_acceptance', ROOT / 'scripts/test-docker.py')
    test = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(test)
    test.FIXTURE = FIXTURE
    test.URL = 'http://127.0.0.1:28085'
    test.PANEL_URL = 'http://127.0.0.1:22057'
    report = {'passed': False, 'from': OLD, 'to': NEW, 'local_only': True, 'checks': []}
    resume = '--resume-migration' in sys.argv
    if FIXTURE.exists() and not resume:
        raise RuntimeError('Upgrade fixture already exists; preserve it before another run')
    FIXTURE.mkdir(parents=True, exist_ok=resume)
    for name in ('x-ui', 'compose.yaml'):
        shutil.copy2(ROOT / 'deploy/docker' / name, FIXTURE / name)
    (FIXTURE / 'bundle.json').write_text(json.dumps({'image': OLD}))

    def mark(label):
        test.record(report['checks'], label)

    def schema(folder):
        with closing(sqlite3.connect('file:' + str(folder / 'panel/x-ui.db') + '?mode=ro', uri=True)) as db:
            return {name: [r[1] for r in db.execute('PRAGMA table_info("' + name + '")')]
                    for (name,) in db.execute("SELECT name FROM sqlite_master WHERE type='table'")}

    def state():
        # Stable identity/configuration and official traffic, without exposing secrets.
        value = {name: json.loads((FIXTURE / 'data/state' / name).read_text())
                 for name in ('deployment.json', 'credentials.json')}
        value['clients'] = test.api('clients/list')
        return hashlib.sha256(json.dumps(value, sort_keys=True).encode()).hexdigest()

    legacy = {'udp': [{'type': 'xdns', 'settings': {
        'domains': [{'name': 't.example.com', 'types': [16], 'edns0': 1232}],
        'resolvers': [{'type': 'udp', 'settings': {'addr': '8.8.8.8:53'}}]}}]}
    migrated = {'udp': [{'type': 'xdns', 'settings': {
        'domains': [{'names': ['t.example.com'], 'types': [16], 'edns0': 1232}],
        'resolvers': [{'addrs': ['udp://8.8.8.8:53']}]}}]}

    def stored_mask(folder):
        with closing(sqlite3.connect('file:' + str(folder / 'panel/x-ui.db') + '?mode=ro', uri=True)) as db:
            return json.loads(db.execute("SELECT value FROM settings WHERE key='subJsonFinalMask'").fetchone()[0])

    try:
        if resume:
            assert 'XUI_IMAGE=' + OLD in (FIXTURE / '.env').read_text()
            report['checks'] = json.loads(REPORT.read_text())['checks'][:3]
            test.cli('start')
        else:
            test.cli('init', '--bind', '127.0.0.1', '--port', '28085', '--panel-port', '22057',
                     '--api-port', '63793', '--metrics-port', '21115', '--base-path', test.BASE,
                     '--project-name', 'x-ui-upgrade102-check')
            test.real_traffic(report['checks'])
        # Use the actual legacy shape from upstream's xdns migration regression.
        # It affects a disabled subscription option and sends no DNS traffic.
        test.cli('stop')
        with closing(sqlite3.connect(FIXTURE / 'data/panel/x-ui.db')) as db, db:
            db.execute("DELETE FROM settings WHERE key='subJsonFinalMask'")
            db.execute("INSERT INTO settings(key,value) VALUES('subJsonFinalMask',?)", (json.dumps(legacy),))
        test.cli('start')
        assert stored_mask(FIXTURE / 'data') == legacy
        # Let the last official stats and the independent sampler settle.
        time.sleep(12)
        before, config_before = test.fingerprint(), state()
        schema_before = schema(FIXTURE / 'data')
        assert before['visits']['count'] == 4 and before['traffic']['count'] > 0
        mark('old release contains actual REALITY visits and sampled traffic before upgrading')

        bad = FIXTURE / 'bad-image'
        bad.mkdir(exist_ok=resume)
        (bad / 'fail-monitor').write_text('#!/bin/sh\nprintf "after-official-startup\\n" > /data/state/migration-failure-probe\nexit 23\n')
        (bad / 'Dockerfile').write_text('FROM ' + NEW + '\nCOPY fail-monitor /app/traffic-monitor\nRUN chmod 755 /app/traffic-monitor\n')
        subprocess.run(['docker', 'build', '--network=host', '-t', BAD, str(bad)],
                       env={**os.environ, 'DOCKER_BUILDKIT': '0'}, check=True, capture_output=True)
        replaced_before = set((FIXTURE / 'backups').glob('replaced-data-*'))
        result = test.cli('update', BAD, check=False)
        assert result.returncode != 0
        replaced = set((FIXTURE / 'backups').glob('replaced-data-*')) - replaced_before
        failed = next(p for p in replaced if (p / 'state/migration-failure-probe').is_file())
        assert json.loads((failed / 'core/release.json').read_text())['panel_version'] == '3.10.2'
        schema_migrated = schema(failed)
        report['official_added_columns'] = {t: sorted(set(cols) - set(schema_before.get(t, [])))
                                            for t, cols in schema_migrated.items()
                                            if set(cols) - set(schema_before.get(t, []))}
        assert stored_mask(failed) == migrated, 'Failure must occur after the actual official data migration'
        report['official_data_migration'] = 'subJsonFinalMask: legacy xdns name/typed resolver -> names/addrs'
        assert schema(FIXTURE / 'data') == schema_before
        assert stored_mask(FIXTURE / 'data') == legacy
        assert test.fingerprint() == before and state() == config_before
        assert 'XUI_IMAGE=' + OLD in (FIXTURE / '.env').read_text()
        assert not (FIXTURE / 'data/state/migration-failure-probe').exists()
        mark('failure after official data migration restores old configuration, schema, image, clients, credentials and history')

        test.cli('update', NEW)
        browser = test.login()
        health = browser.obj(test.BASE + 'traffic-monitor/api/v1/health')
        assert health['adapter_compatible'] and health['detail_days'] == 7 and health['summary_days'] == 35
        assert json.loads((FIXTURE / 'data/core/release.json').read_text())['panel_version'] == '3.10.2'
        assert schema(FIXTURE / 'data') == schema_migrated
        assert stored_mask(FIXTURE / 'data') == migrated
        assert test.fingerprint() == before and state() == config_before
        page = browser.request(test.BASE + 'panel/?monitor=traffic')
        assert page[0] == 200 and 'adapter.js' in str(page)
        assert browser.obj(test.BASE + 'traffic-monitor/api/v1/visits')['total'] == 4
        assert browser.obj(test.BASE + 'traffic-monitor/api/v1/targets')['total'] == 2
        assert browser.obj(test.BASE + 'traffic-monitor/api/v1/summary')['total'] > 0
        mark('real old-to-new migration preserves history, identities, credentials, configuration and grouping')
        test.cli('restart')
        assert test.fingerprint() == before
        mark('post-upgrade restart does not replay or duplicate historical access logs')
        result = test.cli('update', OLD, check=False)
        assert result.returncode != 0 and 'Database downgrade refused' in result.stderr
        assert test.fingerprint() == before
        mark('direct schema downgrade is refused; matching backup is required')
        report['history_preserved'] = before
        report['passed'] = True
    except Exception as exc:
        report['error'] = type(exc).__name__ + ': ' + str(exc)[:300]
        raise
    finally:
        test.cli('remove', '--yes', check=False)
        REPORT.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')


if __name__ == '__main__':
    main()
