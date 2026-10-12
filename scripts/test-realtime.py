#!/usr/bin/env python3
"""Verify live rates with bounded, local VLESS transfers and a dedicated client.

All fixtures stay in the workspace. Existing histories and counters are preserved;
the temporary routing template is restored and the test inbound disabled afterward.
"""
import copy
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
import runpy
import socket
import subprocess
import threading
import time
import uuid

import lab

ROOT = lab.ROOT
EMAIL, TAG = 'monitor-live-check', 'monitor-live-validation'
PORT, SOCKS, HTTP = 15446, 11083, 19093
FOLDER = ROOT / 'artifacts/fixtures/realtime'
Browser = runpy.run_path(str(ROOT / 'scripts/verify-runtime.py'))['Browser']


class Fixture(BaseHTTPRequestHandler):
    def do_GET(self):
        count = 8 * 1024 * 1024
        self.send_response(200)
        self.send_header('Content-Length', str(count))
        self.end_headers()
        try:
            for _ in range(count // 8192):
                self.wfile.write(b'd' * 8192)
                self.wfile.flush()
                time.sleep(.125)
        except (BrokenPipeError, ConnectionResetError):
            pass

    def do_POST(self):
        count = int(self.headers.get('Content-Length', 0))
        assert 0 < count <= 4 * 1024 * 1024
        while count:
            chunk = self.rfile.read(min(count, 4096))
            if not chunk:
                break
            count -= len(chunk)
        try:
            self.send_response(200 if count == 0 else 400)
            self.send_header('Content-Length', '2')
            self.end_headers()
            self.wfile.write(b'ok')
        except (BrokenPipeError, ConnectionResetError):
            pass

    def log_message(self, *args):
        pass


def wait_for(fn, timeout=45):
    until = time.monotonic() + timeout
    while time.monotonic() < until:
        value = fn()
        if value:
            return value
        time.sleep(1)
    raise AssertionError('Timed out waiting for live fixture state')


def main():
    FOLDER.mkdir(parents=True, exist_ok=True)
    report = {'passed': False, 'email': EMAIL, 'protocol': 'local VLESS/TCP', 'samples': []}
    others = lambda: {c['email']: {k: (c.get('traffic') or {}).get(k, 0) for k in ('up', 'down')}
                      for c in lab.api('clients/list') if c['email'] != EMAIL}
    before = others()
    state = lab.state()
    browser = Browser(state['public_url'].replace('localhost', '127.0.0.1'))
    base = state['base_path']
    csrf = browser.obj(base + 'csrf-token')
    browser.obj(base + 'login', {'username': state['username'], 'password': state['password']}, {'X-CSRF-Token': csrf})
    read = lambda route: browser.obj(base + 'traffic-monitor/api/v1/' + route)
    before_visits = read('visits')['total']
    raw = lab.api('xray/', {}, 'POST')
    original = (json.loads(raw) if isinstance(raw, str) else raw)['xraySetting']
    if isinstance(original, str):
        original = json.loads(original)
    template = copy.deepcopy(original)
    template['outbounds'].append({'tag': TAG, 'protocol': 'freedom', 'settings': {
        'finalRules': [{'action': 'allow', 'ip': ['127.0.0.1/32', '::1/128']}, {'action': 'block'}]}})
    template['routing']['rules'].insert(0, {'type': 'field', 'inboundTag': [TAG], 'outboundTag': TAG})
    inbound, proxy, server = None, None, None
    transfers, outputs = [], []
    try:
        inbound = next((x for x in lab.api('inbounds/list') if x['tag'] == TAG), None)
        if not inbound:
            inbound = lab.api('inbounds/add', {'enable': 'true', 'remark': '实时上传下载验证',
                'listen': '127.0.0.1', 'port': PORT, 'protocol': 'vless', 'tag': TAG,
                'settings': json.dumps({'clients': [{'id': str(uuid.uuid4()), 'email': EMAIL, 'enable': True,
                    'flow': '', 'totalGB': 0, 'expiryTime': 0, 'limitIp': 0}], 'decryption': 'none'}),
                'streamSettings': json.dumps({'network': 'tcp', 'security': 'none'}),
                'sniffing': json.dumps({'enabled': False})})
        assert inbound['listen'] == '127.0.0.1' and inbound['port'] == PORT
        settings = json.loads(inbound['settings']) if isinstance(inbound['settings'], str) else inbound['settings']
        client = next(c for c in settings['clients'] if c['email'] == EMAIL)
        lab.api('inbounds/setEnable/' + str(inbound['id']), {'enable': 'true'})
        lab.api('xray/update', {'xraySetting': json.dumps(template)})
        lab.api('server/restartXrayService', {})
        server = ThreadingHTTPServer(('127.0.0.1', HTTP), Fixture)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        config = {'log': {'loglevel': 'warning'}, 'inbounds': [{'listen': '127.0.0.1', 'port': SOCKS,
            'protocol': 'socks', 'settings': {'auth': 'noauth'}}], 'outbounds': [{'protocol': 'vless',
            'settings': {'vnext': [{'address': '127.0.0.1', 'port': PORT, 'users': [{'id': client['id'], 'encryption': 'none'}]}]},
            'streamSettings': {'network': 'tcp'}}]}
        lab.write_private(FOLDER / 'client.json', config)
        with (FOLDER / 'client.log').open('ab') as output:
            proxy = subprocess.Popen([str(lab.PANEL / 'bin/xray-linux-amd64'), 'run', '-config', str(FOLDER / 'client.json')],
                cwd=FOLDER, env=lab.env(), stdout=output, stderr=output)
        def listening():
            try:
                with socket.create_connection(('127.0.0.1', SOCKS), timeout=1):
                    return True
            except OSError:
                return False
        wait_for(listening, 10)
        find_live = lambda: next((c for c in read('realtime')['items'] if c['email'] == EMAIL and c['rate_state'] == 'ok'), None)
        row = wait_for(find_live)
        fixture = {'email': EMAIL, 'client_id': row['id'], 'inbound_id': inbound['id']}
        (FOLDER / 'fixture.json').write_text(json.dumps(fixture))
        (FOLDER / 'upload.bin').write_bytes(b'u' * (4 * 1024 * 1024))
        common = ['curl', '--silent', '--show-error', '--fail', '--max-time', '170', '--noproxy', '',
            '--socks5-hostname', '127.0.0.1:' + str(SOCKS), '-o', os.devnull]
        commands = [common + ['http://localhost:' + str(HTTP) + '/download'],
            common + ['--limit-rate', '32K', '--data-binary', '@' + str(FOLDER / 'upload.bin'), 'http://localhost:' + str(HTTP) + '/upload']]
        for i, cmd in enumerate(commands):
            output = (FOLDER / ('transfer-' + str(i) + '.log')).open('wb')
            outputs.append(output)
            transfers.append(subprocess.Popen(cmd, stdout=output, stderr=output, cwd=FOLDER, env=lab.env()))
        print('Bounded VLESS upload/download started; waiting for online rates.', flush=True)
        def flowing():
            snapshot = read('realtime?client_id=' + fixture['client_id'])
            if not snapshot['items']:
                return None
            current = snapshot['items'][0]
            if current['status'] == 'online' and (current['up_per_second'] or 0) > 0 and (current['down_per_second'] or 0) > 0:
                return snapshot
        snapshot = wait_for(flowing)
        report['samples'].append(snapshot)
        print('Online status and both rate directions verified; checking browser.', flush=True)
        subprocess.run(['node', str(ROOT / 'scripts/browser-tests/realtime.mjs')], cwd=ROOT,
            env={**os.environ, 'REALTIME_FIXTURE': str(FOLDER / 'fixture.json')}, check=True, timeout=130)
        print('Browser checks passed; waiting for bounded transfers to complete.', flush=True)
        for proc in transfers:
            assert proc.wait(timeout=150) == 0, 'Local transfer failed'
        def offline():
            snapshot = read('realtime?client_id=' + fixture['client_id'])
            current = snapshot['items'][0]
            if current['status'] == 'offline' and current['up'] == 0 and current['down'] == 0:
                return snapshot
        report['samples'].append(wait_for(offline, 55))
        current = next(c for c in lab.api('clients/list') if c['email'] == EMAIL)
        counters = current.get('traffic') or {}
        assert counters.get('up', 0) >= 4 * 1024 * 1024
        assert counters.get('down', 0) >= 8 * 1024 * 1024
        assert others() == before, 'Existing official counters changed'
        assert read('visits')['total'] >= before_visits + 2
        report.update(panel_up=counters['up'], panel_down=counters['down'], other_counters_preserved=True,
            upload_payload=4 * 1024 * 1024, download_payload=8 * 1024 * 1024, passed=True)
    finally:
        for proc in transfers + ([proxy] if proxy else []):
            if proc.poll() is None:
                proc.terminate()
                try:
                    proc.wait(timeout=8)
                except subprocess.TimeoutExpired:
                    proc.kill()
                    proc.wait()
        for output in outputs:
            output.close()
        if server:
            server.shutdown()
            server.server_close()
        if inbound:
            lab.api('inbounds/setEnable/' + str(inbound['id']), {'enable': 'false'})
        lab.api('xray/update', {'xraySetting': json.dumps(original)})
        lab.api('server/restartXrayService', {})
        report.update(temporary_routing_restored=True, test_inbound_disabled=True)
        (ROOT / 'artifacts/reports/realtime-traffic.json').write_text(json.dumps(report, indent=2) + '\n')
        print('Temporary routing restored; dedicated test inbound disabled.', flush=True)
    print('Realtime transfer and browser verification passed.', flush=True)


if __name__ == '__main__':
    main()
