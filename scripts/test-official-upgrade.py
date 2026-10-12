#!/usr/bin/env python3
"""Exercise official 3.8.5 -> 3.9.0 migration in an isolated, disposable WSL lab.

No live main-panel databases, runtime files, tokens, ports or monitor files are
used. Authentication secrets stay in memory. The monitor preservation check is
an independent SQLite sentinel, not a claim of a live monitor upgrade test.
"""
from datetime import datetime
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import signal
import socket
import sqlite3
import subprocess
import sys
import tarfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from zoneinfo import ZoneInfo

ROOT = Path(__file__).resolve().parents[1]
BASE = '/upgrade-test/'
ORIGIN = 'http://127.0.0.1:12055'


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def main():
    run = ROOT / 'artifacts/fixtures/version-upgrade' / str(time.time_ns())
    panel, data, logs = run / 'runtime/x-ui', run / 'data/panel', run / 'runtime/logs'
    independent = run / 'data/monitor'
    for path in (panel.parent, data, logs, independent, ROOT / 'cache/tmp', ROOT / 'artifacts/reports'):
        path.mkdir(parents=True, exist_ok=True)
    environment = os.environ.copy()
    environment.update(XUI_BIN_FOLDER=str(panel / 'bin'), XUI_DB_FOLDER=str(data), XUI_LOG_FOLDER=str(logs),
                       XUI_MAIN_FOLDER=str(panel), XUI_LOG_LEVEL='warning', XUI_SKIP_HSTS='true',
                       TZ='Asia/Shanghai', TMPDIR=str(ROOT / 'cache/tmp'), TMP=str(ROOT / 'cache/tmp'),
                       TEMP=str(ROOT / 'cache/tmp'), XDG_CACHE_HOME=str(ROOT / 'cache/xdg'),
                       XDG_CONFIG_HOME=str(ROOT / 'cache/xdg-config'), XDG_DATA_HOME=str(ROOT / 'cache/xdg-data'),
                       PYTHONDONTWRITEBYTECODE='1', GOCACHE=str(ROOT / 'cache/go-build'),
                       GOMODCACHE=str(ROOT / 'cache/go-mod'), GOPATH=str(ROOT / 'cache/go'),
                       npm_config_cache=str(ROOT / 'cache/npm'))
    report = {'checked_at': datetime.now(ZoneInfo('Asia/Shanghai')).isoformat(),
              'from': 'v3.8.5', 'to': 'v3.9.0', 'base_path': BASE,
              'fixture': str(run.relative_to(ROOT)), 'checks': [],
              'limitations': ['Independent monitor preservation uses a separate SQLite sentinel and config file.',
                              'This isolated test does not run the live monitor or generate real proxy traffic.',
                              'Main panel and monitor are not modified by this test.']}
    process = None
    token = ''

    def checked(name, evidence=True):
        report['checks'].append({'name': name, 'status': 'passed', 'evidence': evidence})
        print('PASS', name, flush=True)

    def extract(version):
        archive = ROOT / 'upstream/3x-ui' / version / 'x-ui-linux-amd64.tar.gz'
        expected = archive.with_suffix('.gz.sha256').read_text().split()[0].lower()
        sha = digest(archive)
        assert sha == expected, 'Official SHA256 mismatch'
        with tarfile.open(archive) as stream:
            stream.extractall(panel.parent, filter='data')
        (panel / 'x-ui').chmod(0o755)
        (panel / 'bin/xray-linux-amd64').chmod(0o755)
        return sha

    def cli(*arguments):
        return subprocess.run([str(panel / 'x-ui'), *arguments], cwd=panel, env=environment,
                              capture_output=True, text=True, check=True, timeout=45)

    def api(path, payload=None, json_payload=False):
        headers = {'Authorization': 'Bearer ' + token, 'X-Requested-With': 'XMLHttpRequest', 'Accept': 'application/json'}
        body = None
        if payload is not None:
            if json_payload:
                body = json.dumps(payload).encode()
                headers['Content-Type'] = 'application/json'
            else:
                body = urllib.parse.urlencode(payload).encode()
                headers['Content-Type'] = 'application/x-www-form-urlencoded'
        request = urllib.request.Request(ORIGIN + BASE + 'panel/api/' + path, data=body, headers=headers)
        with urllib.request.urlopen(request, timeout=15) as response:
            value = json.load(response)
        assert value.get('success') is True, 'Official API returned failure at ' + path
        return value.get('obj')

    def start():
        nonlocal process
        with (logs / 'panel.log').open('ab') as output:
            process = subprocess.Popen([str(panel / 'x-ui'), 'run'], cwd=panel, env=environment,
                                       stdin=subprocess.DEVNULL, stdout=output, stderr=subprocess.STDOUT,
                                       start_new_session=True)
        for _ in range(60):
            if process.poll() is not None:
                raise AssertionError('Isolated official panel exited before readiness')
            try:
                status = api('server/status')
                assert isinstance(status, dict)
                return status
            except (OSError, ValueError, AssertionError):
                time.sleep(.5)
        raise AssertionError('Isolated official panel did not become ready')

    def stop():
        nonlocal process
        if process is not None:
            if process.poll() is None:
                process.send_signal(signal.SIGTERM)
                try:
                    process.wait(timeout=20)
                except subprocess.TimeoutExpired:
                    os.killpg(process.pid, signal.SIGTERM)
                    process.wait(timeout=10)
            process = None

    def settings_write(key, value):
        with sqlite3.connect(data / 'x-ui.db') as db:
            updated = db.execute('UPDATE settings SET value=? WHERE key=?', (value, key)).rowcount
            if not updated:
                db.execute('INSERT INTO settings(key,value) VALUES(?,?)', (key, value))

    def template_read():
        raw = api('xray/', {})
        wrapper = json.loads(raw) if isinstance(raw, str) else raw
        result = wrapper['xraySetting']
        return json.loads(result) if isinstance(result, str) else result

    try:
        for port in (12055, 62795, 11115):
            with socket.socket() as probe:
                probe.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
                probe.bind(('127.0.0.1', port))
        checked('isolated-ports-free', [12055, 62795, 11115])
        report['from_sha256'] = extract('v3.8.5')
        username, password = 'upgrade-test-admin', secrets.token_urlsafe(24)
        cli('setting', '-port', '12055', '-listenIP', '127.0.0.1', '-webBasePath', BASE,
            '-username', username, '-password', password)
        # v3.8.5 has no tokenScope CLI flag; its dedicated token is the older
        # unrestricted API token, migrated by v3.9.0 into the admin scope.
        token_output = cli('setting', '-getApiToken', '-tokenName', 'isolated-upgrade-check')
        match = re.search(r'apiToken:\s*(\S+)', token_output.stdout)
        assert match, 'Official CLI did not provide the dedicated token'
        token = match.group(1)
        # The fixture is offline at this point. Set loopback-only defaults before
        # its first launch so it never races the main panel's Xray or subscriptions.
        template = {
            'log': {'access': str(logs / 'xray-access.log'), 'error': str(logs / 'xray-error.log'), 'loglevel': 'warning'},
            'api': {'tag': 'api', 'services': ['HandlerService', 'LoggerService', 'StatsService']},
            'inbounds': [{'tag': 'api', 'listen': '127.0.0.1', 'port': 62795, 'protocol': 'dokodemo-door',
                          'settings': {'address': '127.0.0.1'}}],
            'outbounds': [{'tag': 'direct', 'protocol': 'freedom'}, {'tag': 'blocked', 'protocol': 'blackhole'}],
            'policy': {'levels': {'0': {'statsUserUplink': True, 'statsUserDownlink': True}},
                       'system': {'statsInboundUplink': True, 'statsInboundDownlink': True}},
            'routing': {'rules': [{'type': 'field', 'inboundTag': ['api'], 'outboundTag': 'api'}]},
            'dns': {'hosts': {'upgrade-fixture.test': '127.0.0.1'}},
            'stats': {}, 'metrics': {'tag': 'metrics', 'listen': '127.0.0.1:11115'}}
        settings_write('xrayTemplateConfig', json.dumps(template))
        for key in ('subEnable', 'subJsonEnable', 'subClashEnable'):
            settings_write(key, 'false')
        settings_write('remarkTemplate', 'upgrade-preservation-fixture')
        start()
        checked('official-3.8.5-ready-at-non-root-base')
        before_template = template_read()
        assert before_template['log']['access'] == str(logs / 'xray-access.log')
        fixture_client = {'email': 'upgrade-fixture-client', 'id': str(uuid.uuid4()), 'enable': True,
                          'totalGB': 123456789, 'expiryTime': 0, 'limitIp': 0, 'comment': 'migration sentinel'}
        api('inbounds/add', {'enable': 'false', 'remark': 'isolated upgrade fixture', 'listen': '127.0.0.1',
                             'port': 15445, 'protocol': 'vless', 'tag': 'upgrade-fixture-inbound',
                             'settings': json.dumps({'clients': [fixture_client], 'decryption': 'none'}),
                             'streamSettings': json.dumps({'network': 'tcp', 'security': 'none'}),
                             'sniffing': json.dumps({'enabled': False, 'destOverride': ['http', 'tls']})})
        before_clients = [row for row in api('clients/list') if row['email'] == 'upgrade-fixture-client']
        assert len(before_clients) == 1, 'Fixture client was not created'
        client_before = before_clients[0]
        assert client_before.get('createdAt') and client_before.get('id')
        # Independent extension state deliberately uses another directory/database.
        monitor_db = independent / 'monitor.db'
        with sqlite3.connect(monitor_db) as db:
            db.execute('PRAGMA journal_mode=DELETE')
            db.execute('CREATE TABLE monitor_history(client_id TEXT PRIMARY KEY, up INTEGER, down INTEGER, detail_days INTEGER, summary_days INTEGER)')
            db.execute('INSERT INTO monitor_history VALUES(?,?,?,?,?)', ('fixture-client', 1024, 4096, 7, 35))
        monitor_config = independent / 'config.json'
        monitor_config.write_text(json.dumps({'detail_days': 7, 'summary_days': 35, 'sentinel': 'independent-monitor'}) + '\n')
        monitor_before = {'database': digest(monitor_db), 'configuration': digest(monitor_config)}
        checked('3.8.5-client-and-persistent-template-created')
        stop()
        report['to_sha256'] = extract('v3.9.0')
        # Same fixture DB is now opened and migrated by the new official binary.
        after_status = start()
        checked('official-3.9.0-ready-with-same-database')
        after_template = template_read()
        assert after_template == before_template, 'Persistent Xray template changed during official upgrade'
        rendered = json.loads((panel / 'bin/config.json').read_text())
        assert rendered['log']['access'] == before_template['log']['access']
        assert rendered['metrics']['listen'] == '127.0.0.1:11115'
        assert any(row.get('port') == 62795 for row in rendered.get('inbounds', []))
        checked('persistent-template-and-isolated-xray-addresses-preserved')
        after_clients = [row for row in api('clients/list') if row['email'] == 'upgrade-fixture-client']
        assert len(after_clients) == 1
        stable_fields = ('id', 'createdAt', 'email', 'uuid', 'totalGB', 'comment', 'enable')
        assert all(after_clients[0].get(field) == client_before.get(field) for field in stable_fields)
        checked('client-identity-and-configuration-preserved', list(stable_fields))
        settings = api('setting/all', {})
        assert settings['webBasePath'] == BASE, 'Non-root base path changed'
        assert settings.get('remarkTemplate') == 'upgrade-preservation-fixture', 'Custom remark template changed'
        assert not settings.get('subEnable') and not settings.get('subJsonEnable'), 'Disabled subscriptions were reenabled'
        checked('non-root-base-custom-setting-and-subscription-disable-preserved')
        assert monitor_before == {'database': digest(monitor_db), 'configuration': digest(monitor_config)}
        with sqlite3.connect(monitor_db) as db:
            assert db.execute('SELECT up,down,detail_days,summary_days FROM monitor_history').fetchone() == (1024, 4096, 7, 35)
            assert db.execute('PRAGMA integrity_check').fetchone()[0] == 'ok'
        checked('independent-monitor-sentinel-unchanged', {'sha256_unchanged': True, 'live_monitor': False})
        stop()
        with sqlite3.connect(data / 'x-ui.db') as db:
            assert db.execute('PRAGMA integrity_check').fetchone()[0] == 'ok'
        checked('upgraded-official-database-integrity')
    except Exception as error:
        detail = str(error) if isinstance(error, AssertionError) else ''
        report['checks'].append({'name': 'upgrade-regression', 'status': 'failed', 'error_type': type(error).__name__, 'detail': detail})
        print('FAIL upgrade-regression', type(error).__name__, detail, flush=True)
    finally:
        stop()
        report['passed'] = sum(row['status'] == 'passed' for row in report['checks'])
        report['failed'] = sum(row['status'] == 'failed' for row in report['checks'])
        (ROOT / 'artifacts/reports/version-upgrade.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
        print(f"Report: artifacts/reports/version-upgrade.json; {report['passed']} passed, {report['failed']} failed")
    return 1 if report['failed'] else 0


if __name__ == '__main__':
    sys.exit(main())
