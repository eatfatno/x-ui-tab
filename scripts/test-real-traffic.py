#!/usr/bin/env python3
"""Generate real isolated VLESS traffic for two clients against local HTTP/HTTPS fixtures.

Only local test inbound(s)/temporary routing are created through the official API.
The routing template is restored in finally; clients are retained disabled for review.
"""
import argparse
import copy
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import ssl
import subprocess
import sys
import threading
import time
import urllib.parse
import uuid

sys.path.insert(0, str(Path(__file__).parent))
import lab

ROOT = lab.ROOT
FIXTURES = ROOT / 'artifacts/fixtures/real-traffic'

class Fixture(BaseHTTPRequestHandler):
    def do_GET(self):
        count = int(self.path.rsplit('/', 1)[-1]) if self.path.rsplit('/', 1)[-1].isdigit() else 32768
        count = min(count, 8 * 1024 * 1024)
        self.send_response(200)
        self.send_header('Content-Length', str(count))
        self.end_headers()
        self.wfile.write(b'x' * count)

    def do_POST(self):
        count = int(self.headers.get('Content-Length', 0))
        while count:
            chunk = self.rfile.read(min(count, 65536))
            if not chunk:
                break
            count -= len(chunk)
        self.send_response(200)
        self.send_header('Content-Length', '2')
        self.end_headers()
        self.wfile.write(b'ok')

    def log_message(self, *args):
        pass

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--settle-seconds', type=int, default=25)
    args = parser.parse_args()
    FIXTURES.mkdir(parents=True, exist_ok=True)
    cert, key = FIXTURES / 'fixture.crt', FIXTURES / 'fixture.key'
    if not cert.exists():
        subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes',
                        '-keyout', str(key), '-out', str(cert), '-days', '2',
                        '-subj', '/CN=monitor-fixture.test'], check=True, capture_output=True)
        key.chmod(0o600)
    servers = []
    for port, tls in ((19090, False), (19091, True)):
        server = ThreadingHTTPServer(('127.0.0.1', port), Fixture)
        if tls:
            context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
            context.load_cert_chain(cert, key)
            server.socket = context.wrap_socket(server.socket, server_side=True)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        servers.append(server)
    raw = lab.api('xray/', {}, 'POST')
    original = (json.loads(raw) if isinstance(raw, str) else raw)['xraySetting']
    if isinstance(original, str):
        original = json.loads(original)
    template = copy.deepcopy(original)
    template.setdefault('log', {})['loglevel'] = 'debug'
    template['log']['error'] = str(lab.LOGS / 'xray-test-error.log')
    template['outbounds'].append({'tag': 'monitor-test-local', 'protocol': 'freedom', 'settings': {
        'finalRules': [{'action': 'allow', 'ip': ['127.0.0.1/32', '::1/128']}, {'action': 'block'}]}})
    template['routing']['rules'].insert(0, {'type': 'field', 'inboundTag': ['monitor-test-inbound'], 'outboundTag': 'monitor-test-local'})
    procs, inbound, reports = [], None, []
    try:
        lab.api('xray/update', {'xraySetting': json.dumps(template)})
        clients = [{'id': str(uuid.uuid4()), 'email': 'monitor-test-' + label, 'enable': True,
                    'flow': '', 'totalGB': 0, 'expiryTime': 0, 'limitIp': 0} for label in ('alice', 'bob')]
        for old in lab.api('clients/list') or []:
            if old.get('email') in ('monitor-test-alice', 'monitor-test-bob'):
                lab.api('clients/del/' + urllib.parse.quote(old['email'], safe=''), {})
        for old in lab.api('inbounds/list') or []:
            if old.get('tag') == 'monitor-test-inbound':
                lab.api('inbounds/del/' + str(old['id']), {})
        inbound = lab.api('inbounds/add', {
            'enable': 'true', 'remark': '本地验收 · 两个测试客户端', 'listen': '127.0.0.1',
            'port': 15443, 'protocol': 'vless', 'tag': 'monitor-test-inbound',
            'settings': json.dumps({'clients': clients, 'decryption': 'none'}),
            'streamSettings': json.dumps({'network': 'tcp', 'security': 'none'}),
            'sniffing': json.dumps({'enabled': False, 'destOverride': ['http', 'tls']})})
        # Deleting a prior inbound also removes routing references to its tag.
        # Apply the fixture override after the replacement inbound exists.
        lab.api('xray/update', {'xraySetting': json.dumps(template)})
        lab.api('server/restartXrayService', {})
        for i, client in enumerate(clients):
            config = {
                'log': {'loglevel': 'warning'},
                'inbounds': [{'listen': '127.0.0.1', 'port': 11080 + i, 'protocol': 'socks', 'settings': {'auth': 'noauth', 'udp': True}}],
                'outbounds': [{'protocol': 'vless', 'settings': {'vnext': [{'address': '127.0.0.1', 'port': 15443,
                    'users': [{'id': client['id'], 'encryption': 'none'}]}]}, 'streamSettings': {'network': 'tcp'}}]}
            file = FIXTURES / ('client-' + str(i) + '.json')
            lab.write_private(file, config)
            output = (FIXTURES / ('client-' + str(i) + '.log')).open('ab')
            process = subprocess.Popen([str(lab.PANEL / 'bin/xray-linux-amd64'), 'run', '-config', str(file)],
                                        cwd=FIXTURES, env=lab.env(), stdout=output, stderr=output)
            output.close()
            procs.append(process)
        # Establish monitor baseline before transfers.
        print('Two local VLESS clients ready; waiting for baseline.', flush=True)
        time.sleep(12)
        for i, client in enumerate(clients):
            download = (i + 1) * 1048576
            upload = (i + 1) * 262144
            common = ['curl', '--silent', '--show-error', '--fail', '--max-time', '10',
                      '--noproxy', '', '--socks5-hostname', '127.0.0.1:' + str(11080 + i), '-o', os.devnull]
            for url in ('http://127.0.0.1:19090/bytes/16384', 'http://localhost:19090/bytes/' + str(download),
                        'https://localhost:19091/bytes/32768'):
                transfer = subprocess.run(common + ['--insecure', url], capture_output=True)
                if transfer.returncode:
                    raise RuntimeError('Fixture transfer failed: ' + transfer.stderr.decode(errors='replace'))
            subprocess.run(common + ['--data-binary', '@-', 'http://localhost:19090/upload'],
                           input=b'u' * upload, check=True, capture_output=True)
            reports.append({'email': client['email'], 'http_download_payload': download,
                            'https_download_payload': 32768, 'ip_download_payload': 16384,
                            'upload_payload': upload, 'requests': 4})
        print('HTTP, HTTPS, IP and upload requests succeeded for both clients; settling statistics.', flush=True)
        time.sleep(args.settle_seconds)
        counters = {c['email']: c.get('traffic') for c in lab.api('clients/list') if c['email'].startswith('monitor-test-')}
        for item in reports:
            traffic = counters.get(item['email']) or {}
            item['panel_up'] = traffic.get('up', 0)
            item['panel_down'] = traffic.get('down', 0)
            assert item['panel_up'] >= item['upload_payload'], 'upload direction/count mismatch'
            assert item['panel_down'] >= item['http_download_payload'] + 49152, 'download direction/count mismatch'
        result = {'passed': True, 'clients': reports, 'note': 'Payload sizes exclude protocol overhead; these are real local VLESS connections.'}
        (ROOT / 'artifacts/reports/real-traffic.json').write_text(json.dumps(result, indent=2) + '\n')
        print(json.dumps(result, ensure_ascii=False), flush=True)
    finally:
        for p in procs:
            p.terminate()
            try:
                p.wait(timeout=10)
            except subprocess.TimeoutExpired:
                p.kill()
                p.wait()
        if inbound:
            lab.api('inbounds/setEnable/' + str(inbound['id']), {'enable': 'false'})
        lab.api('xray/update', {'xraySetting': json.dumps(original)})
        lab.api('server/restartXrayService', {})
        for server in servers:
            server.shutdown()
        print('Temporary routing restored and test inbound disabled.', flush=True)

if __name__ == '__main__':
    main()
