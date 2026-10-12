#!/usr/bin/env python3
"""Isolated Docker acceptance. Never reads native panel credentials or history."""
from datetime import datetime, timedelta, timezone
from contextlib import closing
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import copy
import hashlib
import json
import os
from pathlib import Path
import re
import re
import runpy
import shutil
import socket
import sqlite3
import subprocess
import sys
import tarfile
import threading
import time
import urllib.parse
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]
fixture_name = os.environ.get('DOCKER_TEST_FIXTURE', 'docker-acceptance')
if not re.fullmatch(r'docker-acceptance[a-z0-9-]*', fixture_name):
    raise ValueError('DOCKER_TEST_FIXTURE must be a name starting with docker-acceptance')
FIXTURE = ROOT / 'artifacts/fixtures' / fixture_name
REPORT = ROOT / 'artifacts/reports/docker-acceptance.json'
Browser = runpy.run_path(str(ROOT / 'scripts/verify-runtime.py'))['Browser']
BASE = '/docker-check/'
URL = 'http://127.0.0.1:28080'


def free_port():
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]


def cli(*args, data=None, check=True):
    p = subprocess.run([sys.executable, str(FIXTURE / 'x-ui'), *map(str, args)], cwd=FIXTURE,
                       input=data, text=True, capture_output=True, timeout=240)
    with (FIXTURE / 'management.log').open('a') as f:
        f.write(p.stdout + p.stderr)
    if check and p.returncode:
        raise RuntimeError('Management command failed: ' + str(args[0]))
    return p


def api(route, data=None):
    headers = {'Authorization': 'Bearer ' + (FIXTURE / 'data/state/api-token').read_text().strip(), 'X-Requested-With': 'XMLHttpRequest'}
    req = urllib.request.Request('http://127.0.0.1:22053' + BASE + 'panel/api/' + route,
        data=urllib.parse.urlencode(data).encode() if data is not None else None, headers=headers)
    with urllib.request.urlopen(req, timeout=15) as r:
        result = json.load(r)
    assert result['success'], 'Official API request failed'
    return result.get('obj')


def login():
    creds = json.loads((FIXTURE / 'data/state/credentials.json').read_text())
    b = Browser(URL)
    csrf = b.obj(BASE + 'csrf-token')
    b.obj(BASE + 'login', creds, {'X-CSRF-Token': csrf})
    return b


def record(checks, label):
    checks.append(label)
    print('PASS', label, flush=True)


def fingerprint():
    with closing(sqlite3.connect('file:' + str(FIXTURE / 'data/monitor/monitor.db') + '?mode=ro', uri=True)) as db:
        result = {}
        for table in ('visits', 'traffic', 'visit_counts'):
            rows = sorted(db.execute('SELECT * FROM ' + table).fetchall())
            result[table] = {'count': len(rows), 'sha256': hashlib.sha256(json.dumps(rows).encode()).hexdigest()}
        return result


class Payload(BaseHTTPRequestHandler):
    def do_GET(self):
        payload = b'd' * (1024 * 1024)
        self.send_response(200); self.send_header('Content-Length', str(len(payload))); self.end_headers()
        self.wfile.write(payload)

    def do_POST(self):
        self.rfile.read(int(self.headers['Content-Length']))
        self.send_response(200); self.send_header('Content-Length', '2'); self.end_headers(); self.wfile.write(b'ok')

    def log_message(self, *_):
        pass


def real_traffic(checks):
    xray = ROOT / 'artifacts/docker/context/panel/bin/xray-linux-amd64'
    http = ThreadingHTTPServer(('127.0.0.1', 0), Payload)
    threading.Thread(target=http.serve_forever, daemon=True).start()
    children, files = [], []
    tls_port, inbound_port = free_port(), free_port()
    cert, key = FIXTURE / 'fixture.crt', FIXTURE / 'fixture.key'
    subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', str(key), '-out', str(cert),
                    '-days', '2', '-subj', '/CN=docker.test', '-addext', 'subjectAltName=DNS:docker.test,IP:127.0.0.1'], check=True, capture_output=True)
    def launch(name, args):
        f = (FIXTURE / (name + '.log')).open('wb'); files.append(f)
        p = subprocess.Popen(args, stdout=f, stderr=f, cwd=FIXTURE); children.append(p)
        return p
    try:
        launch('tls-target', ['openssl', 's_server', '-quiet', '-accept', '127.0.0.1:' + str(tls_port), '-cert', str(cert), '-key', str(key), '-tls1_3', '-alpn', 'h2', '-www'])
        keys = subprocess.check_output([str(xray), 'x25519'], text=True)
        values = dict(line.split(': ', 1) for line in keys.splitlines() if ': ' in line)
        pub = values.get('Password (PublicKey)') or values.get('Password') or values.get('PublicKey')
        clients = [{'id': str(uuid.uuid4()), 'email': 'monitor-test-' + n, 'subId': uuid.uuid4().hex[:16], 'enable': True,
                    'flow': 'xtls-rprx-vision', 'totalGB': 0, 'expiryTime': 0} for n in ('alice', 'bob')]
        sid = os.urandom(8).hex()
        inbound = api('inbounds/add', {'enable': 'true', 'remark': '本地验收 · Docker REALITY', 'tag': 'docker-reality-check',
            'listen': '127.0.0.1', 'port': inbound_port, 'protocol': 'vless', 'settings': json.dumps({'clients': clients, 'decryption': 'none'}),
            'streamSettings': json.dumps({'network': 'tcp', 'security': 'reality', 'realitySettings': {'target': '127.0.0.1:' + str(tls_port),
                'serverNames': ['docker.test'], 'privateKey': values['PrivateKey'], 'shortIds': [sid]}}),
            'sniffing': json.dumps({'enabled': True, 'destOverride': ['http', 'tls'], 'routeOnly': True})})
        raw = api('xray/', {})
        original = (json.loads(raw) if isinstance(raw, str) else raw)['xraySetting']
        if isinstance(original, str): original = json.loads(original)
        template = copy.deepcopy(original)
        template['outbounds'].append({'tag': 'docker-local-only', 'protocol': 'freedom', 'settings': {
            'finalRules': [{'action': 'allow', 'ip': ['127.0.0.1/32', '::1/128']}, {'action': 'block'}]}})
        template['routing']['rules'].insert(0, {'type': 'field', 'inboundTag': ['docker-reality-check'], 'outboundTag': 'docker-local-only'})
        api('xray/update', {'xraySetting': json.dumps(template)})
        api('server/restartXrayService', {})
        socks = [free_port(), free_port()]
        for i, client in enumerate(clients):
            cfg = {'log': {'loglevel': 'warning'}, 'inbounds': [{'listen': '127.0.0.1', 'port': socks[i], 'protocol': 'socks', 'settings': {'auth': 'noauth'}}],
                'outbounds': [{'protocol': 'vless', 'settings': {'vnext': [{'address': '127.0.0.1', 'port': inbound_port,
                    'users': [{'id': client['id'], 'encryption': 'none', 'flow': 'xtls-rprx-vision'}]}]},
                    'streamSettings': {'network': 'tcp', 'security': 'reality', 'realitySettings': {'fingerprint': 'chrome',
                        'serverName': 'docker.test', 'publicKey': pub, 'shortId': sid}}}]}
            file = FIXTURE / ('client-' + str(i) + '.json'); file.write_text(json.dumps(cfg)); file.chmod(0o600)
            launch('driver-' + str(i), [str(xray), 'run', '-config', str(file)])
        time.sleep(12)
        for port in socks:
            common = ['curl', '--silent', '--show-error', '--fail', '--max-time', '15', '--noproxy', '',
                      '--socks5-hostname', '127.0.0.1:' + str(port), '-o', os.devnull]
            subprocess.run(common + ['http://localhost:' + str(http.server_port) + '/download'], check=True, capture_output=True)
            subprocess.run(common + ['--data-binary', '@-', 'http://localhost:' + str(http.server_port) + '/upload'],
                           input=b'u' * 131072, check=True, capture_output=True)
        time.sleep(25)
        b = login()
        visits = b.obj(BASE + 'traffic-monitor/api/v1/visits')
        assert visits['total'] == 4 and {v['email'] for v in visits['items']} == {c['email'] for c in clients}
        assert all(v['target'] == 'localhost' for v in visits['items'])
        summaries = b.obj(BASE + 'traffic-monitor/api/v1/targets')
        assert summaries['total'] == 2 and all(t['visit_count'] == 2 for t in summaries['items'])
        for c in api('clients/list'):
            assert c['traffic']['up'] >= 131072 and c['traffic']['down'] >= 1048576
        summary = b.obj(BASE + 'traffic-monitor/api/v1/summary')
        assert summary['up'] >= 262144 and summary['down'] >= 2097152
        record(checks, 'two real VLESS/TCP/REALITY clients: attribution, upload/download and grouped targets')
        api('inbounds/setEnable/' + str(inbound['id']), {'enable': 'false'})
        api('xray/update', {'xraySetting': json.dumps(original)})
        api('server/restartXrayService', {})
    finally:
        for p in reversed(children):
            p.terminate()
            try: p.wait(timeout=5)
            except subprocess.TimeoutExpired: p.kill(); p.wait()
        for f in files: f.close()
        http.shutdown(); http.server_close()


def browser_checks():
    creds = json.loads((FIXTURE / 'data/state/credentials.json').read_text())
    state = FIXTURE / 'browser-state.json'
    state.write_text(json.dumps(creds | {'base_path': BASE, 'public_url': URL}))
    state.chmod(0o600)
    env = dict(os.environ, PANEL_STATE_FILE=str(state), BROWSER_REPORT_FILE=str(ROOT / 'artifacts/reports/docker-official-browser.json'), QR_CLIENT='docker-qr-client')
    inbound = next(i for i in api('inbounds/list') if i['tag'] == 'docker-reality-check')
    api('inbounds/setEnable/' + str(inbound['id']), {'enable': 'true'})
    # Official v3.9 deliberately hides QR images for post-quantum REALITY
    # links. Use a plain VLESS fixture to exercise the actual QR renderer.
    qr = api('inbounds/add', {'enable': 'true', 'remark': 'QR fixture', 'tag': 'docker-qr-fixture', 'listen': '127.0.0.1',
        'port': free_port(), 'protocol': 'vless', 'settings': json.dumps({'decryption': 'none', 'clients': [{
            'id': str(uuid.uuid4()), 'email': 'docker-qr-client', 'subId': uuid.uuid4().hex[:16], 'enable': True, 'flow': ''}]}),
        'streamSettings': json.dumps({'network': 'tcp', 'security': 'none'}), 'sniffing': json.dumps({'enabled': False})})
    try:
        subprocess.run(['node', str(ROOT / 'scripts/browser-tests/official-panel.mjs')], cwd=ROOT, env=env, check=True, timeout=480)
        subprocess.run(['node', str(ROOT / 'scripts/browser-tests/docker-monitor.mjs')], cwd=ROOT, env=env, check=True, timeout=180)
    finally:
        api('inbounds/del/' + str(qr['id']), {})
        api('clients/del/docker-qr-client', {})
        api('inbounds/setEnable/' + str(inbound['id']), {'enable': 'false'})


def main():
    global BASE, URL
    report = {'passed': False, 'checks': [], 'local_only': True, 'native_environment_untouched': True}
    resume = '--resume-browser' in sys.argv
    if FIXTURE.exists() and not resume:
        raise RuntimeError('Fixture already exists; preserve reports and use a separate fixture for a new run')
    FIXTURE.mkdir(parents=True, exist_ok=resume)
    for name in ('x-ui', 'compose.yaml', 'install.sh'):
        shutil.copy2(ROOT / 'deploy/docker' / name, FIXTURE / name)
    stats_proc = None
    stats_file = None
    try:
        if resume:
            assert 'COMPOSE_PROJECT_NAME=x-ui-docker-check' in (FIXTURE / '.env').read_text()
            previous = json.loads(REPORT.read_text())
            report['checks'] = previous['checks'][:2]
            cli('start')
        else:
            cli('init', '--bind', '127.0.0.1', '--port', '28080', '--panel-port', '22053', '--api-port', '63789',
                '--metrics-port', '21111', '--base-path', BASE, '--project-name', 'x-ui-docker-check')
        assert Browser(URL).request(BASE + 'traffic-monitor/api/v1/clients')[0] == 401
        b = login()
        health = b.obj(BASE + 'traffic-monitor/api/v1/health')
        assert health['detail_days'] == 7 and health['summary_days'] == 35 and health['adapter_compatible']
        if not resume:
            record(report['checks'], 'fresh initialization, random credentials, shared login and 7/35-day configuration')
        stats_file = (FIXTURE / 'docker-stats.jsonl').open('w')
        stats_proc = subprocess.Popen(['docker', 'stats', '--format', '{{json .}}', 'x-ui-docker-check-panel-1'], stdout=stats_file, stderr=subprocess.DEVNULL)
        if not resume:
            real_traffic(report['checks'])
        browser_checks()
        record(report['checks'], 'official lazy modules/QR/sidebar and embedded monitor browser checks')
        before = fingerprint()
        cli('restart'); assert fingerprint() == before
        cli('restart-xray'); cli('rotate-token'); login()
        assert fingerprint() == before
        record(report['checks'], 'graceful restart, Xray restart and token rotation preserve history')
        cli('credentials', '--username', 'docker-changed', '--password-stdin', data=uuid.uuid4().hex + '\n')
        login(); record(report['checks'], 'official username/password reset over private stdin')
        cli('base-path', '/changed/'); BASE = '/changed/'
        login(); cli('port', '28081'); URL = 'http://127.0.0.1:28081'; login()
        assert fingerprint() == before
        record(report['checks'], 'base path and public port changes keep monitor and panel synchronized')
        cli('tls', FIXTURE / 'fixture.crt', FIXTURE / 'fixture.key')
        # Validate trust, HTTPS login and Secure cookies against the imported cert.
        import ssl
        secure_url = URL.replace('http:', 'https:')
        jar = __import__('http.cookiejar', fromlist=['CookieJar']).CookieJar()
        opener = urllib.request.build_opener(urllib.request.HTTPSHandler(context=ssl.create_default_context(cafile=str(FIXTURE / 'fixture.crt'))),
                                            urllib.request.HTTPCookieProcessor(jar))
        with opener.open(secure_url + BASE + 'csrf-token') as r: csrf = json.load(r)['obj']
        creds = json.loads((FIXTURE / 'data/state/credentials.json').read_text())
        req = urllib.request.Request(secure_url + BASE + 'login', data=urllib.parse.urlencode(creds).encode(), headers={'X-CSRF-Token': csrf})
        with opener.open(req) as r: assert json.load(r)['success']
        assert all(c.secure for c in jar if c.name == '3x-ui')
        with opener.open(secure_url + BASE + 'traffic-monitor/api/v1/summary') as r: assert json.load(r)['success']
        cli('tls-off'); login(); record(report['checks'], 'TLS certificate validation, HTTPS session and Secure cookies')
        cli('autostart', 'off'); cli('autostart', 'on')
        assert 'RESTART_POLICY=unless-stopped' in (FIXTURE / '.env').read_text()
        cli('backup')
        backup = sorted((FIXTURE / 'backups').glob('*.tar.gz'))[-1]
        browser = login()
        now = datetime.now(timezone.utc).replace(second=0, microsecond=0)
        query = urllib.parse.urlencode({'start': (now - timedelta(days=1)).isoformat(), 'end': (now + timedelta(minutes=1)).isoformat()})
        preview = browser.obj(BASE + 'traffic-monitor/api/v1/history/preview?' + query)
        assert preview['visit_count'] == 4 and preview['total'] > 0
        token = preview['preview_token']
        req = urllib.request.Request(URL + BASE + 'traffic-monitor/api/v1/history/delete',
            data=json.dumps({'preview_token': token}).encode(), headers={'Content-Type': 'application/json', 'Origin': URL, 'X-Monitor-CSRF': token})
        with browser.opener.open(req) as r: assert json.load(r)['success']
        assert browser.obj(BASE + 'traffic-monitor/api/v1/summary')['total'] == 0
        assert browser.obj(BASE + 'traffic-monitor/api/v1/visits')['total'] == 0
        record(report['checks'], 'minute-scoped deletion removes fixture visits, counts and sampled traffic together')
        cli('base-path', '/restore-check/'); BASE = '/restore-check/'
        cli('restore', backup, '--yes'); BASE = '/changed/'; login()
        assert fingerprint() == before
        record(report['checks'], 'autostart configuration and consistent backup/restore')
        cli('update', 'x-ui-tab:3.9.0-1.0.1-amd64'); login()
        assert fingerprint() == before
        record(report['checks'], 'image recreation preserves panel clients, monitor identity and retained history')
        bad_context = FIXTURE / 'bad-image'
        bad_context.mkdir()
        (bad_context / 'Dockerfile').write_text('FROM x-ui-tab:3.9.0-1.0.1-amd64\nCMD ["deliberate-startup-failure"]\n')
        subprocess.run(['docker', 'build', '--network=host', '-t', 'x-ui-tab:acceptance-failure', str(bad_context)],
            env={**os.environ, 'DOCKER_BUILDKIT': '0'}, check=True, capture_output=True)
        result = cli('update', 'x-ui-tab:acceptance-failure', check=False)
        assert result.returncode != 0
        assert 'XUI_IMAGE=x-ui-tab:3.9.0-1.0.1-amd64' in (FIXTURE / '.env').read_text()
        login(); assert fingerprint() == before
        record(report['checks'], 'failed image startup automatically restores the previous image and database snapshot')
        subprocess.run(['bash', str(FIXTURE / 'install.sh'), str(FIXTURE / 'test-bin')], check=True, capture_output=True)
        menu = subprocess.run([str(FIXTURE / 'test-bin/x-ui')], input='0\n', text=True, capture_output=True, check=True)
        assert 'Docker 管理菜单' in menu.stdout and '重建监测采集 Token' in menu.stdout
        record(report['checks'], 'installed Linux x-ui symlink and interactive menu')
        report['history_rows_preserved'] = fingerprint()
        report['docker_stats'] = subprocess.check_output(['docker', 'stats', '--no-stream', '--format', '{{json .}}'], text=True).strip()
        cli('remove', '--yes')
        assert (FIXTURE / 'data/monitor/monitor.db').exists()
        record(report['checks'], 'uninstall removes only the test container and preserves data')
        report['passed'] = True
    except Exception as e:
        report['error'] = type(e).__name__ + ': ' + str(e)[:300]
        raise
    finally:
        if stats_proc:
            stats_proc.terminate(); stats_proc.wait(timeout=5); stats_file.close()
            samples = []
            for line in (FIXTURE / 'docker-stats.jsonl').read_text().splitlines():
                line = line[line.find('{'):]
                try:
                    row = json.loads(line)
                    # Docker emits '--' while a container is being removed/restarted.
                    if row.get('MemUsage') and re.fullmatch(r'[0-9]+(?:\.[0-9]+)?%', row.get('CPUPerc', '')):
                        samples.append(row)
                except ValueError:
                    pass
            def memory(value):
                import re
                m = re.fullmatch(r'([0-9.]+)([A-Za-z]+)', value.split(' / ')[0])
                return float(m[1]) * {'B': 1, 'KiB': 1024, 'MiB': 1024**2, 'GiB': 1024**3}[m[2]] if m else 0
            if samples:
                report['functional_test_resources'] = {'sample_count': len(samples),
                    'peak_container_memory_bytes': max(memory(s['MemUsage']) for s in samples),
                    'peak_cpu_percent': max(float(s['CPUPerc'].rstrip('%')) for s in samples),
                    'scope': 'Container browser operations and management (includes transfers on a fresh run); not a sustained load test; excludes Docker daemon and OS.'}
        cli('stop', check=False)
        REPORT.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')


if __name__ == '__main__':
    main()
