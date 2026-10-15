#!/usr/bin/env python3
"""Bounded five-client REALITY transfer profile; all traffic stays on loopback.

Uses disposable Xray processes and TLS target. Never loads private user exits,
changes the running panel, or writes to real monitor history.
"""
from concurrent.futures import ThreadPoolExecutor
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import socket
import subprocess
import threading
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]
STAGED_XRAY = ROOT / 'artifacts/docker/context/panel/bin/xray-linux-amd64'
XRAY = Path(os.environ.get('PROFILE_XRAY') or str(STAGED_XRAY if STAGED_XRAY.is_file() else ROOT / 'runtime/panel/bin/xray-linux-amd64'))


def free_port():
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]


def snapshot(pid):
    folder = Path('/proc') / str(pid)
    fields = (folder/'stat').read_text().split(') ', 1)[1].split()
    status = dict(line.split(':', 1) for line in (folder/'status').read_text().splitlines() if ':' in line)
    return ((int(fields[11])+int(fields[12]))/os.sysconf('SC_CLK_TCK'), int(status['VmRSS'].split()[0])*1024)


class Payload(BaseHTTPRequestHandler):
    def do_GET(self):
        seconds, rate = map(int, self.path.strip('/').split('/'))
        assert 0 < seconds <= 35 and 0 < rate <= 3_000_000
        total = seconds * rate
        self.send_response(200)
        self.send_header('Content-Length', str(total))
        self.end_headers()
        start = time.monotonic()
        sent = 0
        try:
            while sent < total:
                part = min(65536, total-sent)
                self.wfile.write(b'p'*part)
                self.wfile.flush()
                sent += part
                delay = start+sent/rate-time.monotonic()
                if delay > 0:
                    time.sleep(delay)
        except (ConnectionResetError, BrokenPipeError):
            pass

    def log_message(self, *_):
        pass


def main():
    folder = ROOT / 'artifacts/fixtures' / ('reality-profile-'+str(time.time_ns()))
    folder.mkdir(parents=True)
    report = {'passed': False, 'local_only': True, 'clients': 5, 'server_cpu_affinity': 1,
              'protocol': 'VLESS/TCP/REALITY/xtls-rprx-vision', 'stages': [],
              'note': '5 distinct clients; bounded HTTP payload over encrypted REALITY on loopback. Shared WSL CPU, not a VPS throughput guarantee. Server excludes client driver, TLS decoy, HTTP source and OS.'}
    report['xray_version'] = subprocess.check_output([str(XRAY), 'version'], text=True).splitlines()[0]
    procs = []
    logs = []
    http = ThreadingHTTPServer(('127.0.0.1', 0), Payload)
    threading.Thread(target=http.serve_forever, daemon=True).start()
    def launch(name, cmd, env=None):
        log = (folder/(name+'.log')).open('wb');logs.append(log)
        p = subprocess.Popen(cmd, cwd=folder, env=env, stdin=subprocess.DEVNULL, stdout=log, stderr=log)
        procs.append(p)
        return p
    def write(name, obj):
        p = folder/name;p.write_text(json.dumps(obj));p.chmod(0o600);return str(p)
    def ready(port, proc):
        for _ in range(40):
            if proc.poll() is not None:
                raise RuntimeError('Disposable process exited; inspect fixture logs')
            try:
                with socket.create_connection(('127.0.0.1', port), .2):return
            except OSError:time.sleep(.1)
        raise RuntimeError('Disposable listener failed to start')
    try:
        tls_port, reality_port = free_port(), free_port()
        subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', str(folder/'key.pem'),
            '-out', str(folder/'cert.pem'), '-days', '1', '-subj', '/CN=capacity.local',
            '-addext', 'subjectAltName=DNS:capacity.local'], check=True, capture_output=True)
        (folder/'key.pem').chmod(0o600)
        tls = launch('tls-target', ['openssl', 's_server', '-quiet', '-accept', '127.0.0.1:'+str(tls_port),
            '-cert', str(folder/'cert.pem'), '-key', str(folder/'key.pem'), '-tls1_3', '-alpn', 'h2', '-www'])
        ready(tls_port, tls)
        result = subprocess.run([str(XRAY), 'x25519'], check=True, capture_output=True, text=True)
        keys = dict(line.split(': ', 1) for line in result.stdout.splitlines() if ': ' in line)
        public = keys.get('Password (PublicKey)') or keys.get('Password') or keys.get('PublicKey')
        clients = [{'id':str(uuid.uuid4()),'email':f'capacity-{i}','flow':'xtls-rprx-vision'} for i in range(5)]
        sid = os.urandom(8).hex()
        server_config = {'log':{'loglevel':'warning','access':str(folder/'access.log')},
            'inbounds':[{'listen':'127.0.0.1','port':reality_port,'protocol':'vless','settings':{'clients':clients,'decryption':'none'},
                'streamSettings':{'network':'tcp','security':'reality','realitySettings':{'target':f'127.0.0.1:{tls_port}',
                    'serverNames':['capacity.local'],'privateKey':keys['PrivateKey'],'shortIds':[sid]}}}],
            'outbounds':[{'protocol':'freedom','settings':{'finalRules':[
                {'action':'allow','ip':['127.0.0.1/32','::1/128']},{'action':'block'}]}}]}
        env = {**os.environ, 'GOMAXPROCS':'1', 'GOMEMLIMIT':'192MiB'}
        server = launch('server', ['taskset','-c',str(min(os.sched_getaffinity(0))),str(XRAY),'run','-config',write('server.json',server_config)], env)
        ready(reality_port, server)
        ports = [free_port() for _ in range(5)]
        config = {'log':{'loglevel':'warning'},'inbounds':[],'outbounds':[],'routing':{'rules':[]}}
        for i, (client, port) in enumerate(zip(clients, ports)):
            tag = f'capacity-{i}'
            config['inbounds'].append({'listen':'127.0.0.1','port':port,'tag':tag,'protocol':'socks','settings':{'auth':'noauth'}})
            config['outbounds'].append({'tag':tag,'protocol':'vless','settings':{'vnext':[{'address':'127.0.0.1','port':reality_port,
                'users':[{'id':client['id'],'encryption':'none','flow':'xtls-rprx-vision'}]}]},
                'streamSettings':{'network':'tcp','security':'reality','realitySettings':{'fingerprint':'chrome','serverName':'capacity.local','publicKey':public,'shortId':sid}}})
            config['routing']['rules'].append({'type':'field','inboundTag':[tag],'outboundTag':tag})
        driver = launch('client', [str(XRAY),'run','-config',write('client.json',config)])
        for port in ports:ready(port, driver)
        def transfer(port, rate, seconds):
            r = subprocess.run(['curl','--silent','--show-error','--fail','--noproxy','','--socks5-hostname',f'127.0.0.1:{port}',
                '--max-time','50','-o',os.devnull,'-w','%{size_download}',f'http://127.0.0.1:{http.server_port}/{seconds}/{rate}'],capture_output=True,text=True)
            if r.returncode != 0:raise RuntimeError('Local REALITY transfer failed: '+r.stderr[:200])
            return int(r.stdout)
        # Fail quickly if the local TLS target cannot negotiate REALITY.
        assert transfer(ports[0], 1024, 1) == 1024
        for mbps in (20,100):
            print(f'Profiling 5 local REALITY clients at {mbps} Mbps total for 30 seconds.', flush=True)
            rate = mbps*1_000_000//8//5
            before, peak = snapshot(server.pid)
            start = sampled_at = time.monotonic();last_cpu = before;peak_cpu = 0
            with ThreadPoolExecutor(max_workers=5) as pool:
                futures = [pool.submit(transfer, port, rate, 30) for port in ports]
                while not all(f.done() for f in futures):
                    time.sleep(1)
                    cpu, rss = snapshot(server.pid);at = time.monotonic()
                    peak_cpu = max(peak_cpu,(cpu-last_cpu)/(at-sampled_at)*100);peak = max(peak,rss)
                    sampled_at,last_cpu = at,cpu
                total = sum(f.result() for f in futures)
            elapsed = time.monotonic()-start
            report['stages'].append({'target_mbps':mbps,'transferred_bytes':total,'seconds':elapsed,'actual_mbps':total*8/elapsed/1e6,
                'cpu_percent_one_core':(last_cpu-before)/elapsed*100,'peak_cpu_percent_one_core':peak_cpu,'peak_rss_bytes':peak})
        report['passed'] = True
    finally:
        for p in reversed(procs):
            if p.poll() is None:
                p.terminate()
                try:p.wait(timeout=5)
                except subprocess.TimeoutExpired:p.kill();p.wait()
        for log in logs:log.close()
        http.shutdown();http.server_close()
        report['temporary_processes_stopped'] = all(p.poll() is not None for p in procs)
        (ROOT/'artifacts/reports/resource-reality-5clients.json').write_text(json.dumps(report,indent=2)+'\n')
    print(json.dumps(report,indent=2))


if __name__ == '__main__':main()
