#!/usr/bin/env python3
"""Bootstrap and supervise the unmodified official panel and independent monitor.

All writes are under /data. Administrative changes run with the main service
stopped. The Xray template is changed transactionally before Xray is started.
"""
import json
from contextlib import closing
import os
from pathlib import Path
import re
import secrets
import shutil
import signal
import sqlite3
import ssl
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

DATA = Path('/data')
APP = Path('/app')
CONFIG = DATA / 'state/deployment.json'
DB = DATA / 'panel/x-ui.db'
TOKEN = DATA / 'state/api-token'
MANIFEST = json.loads((APP / 'manifest.json').read_text())
DEFAULTS = dict(bind='0.0.0.0', port=18080, panel_port=12053,
                xray_api_port=62789, metrics_port=11111, base_path='/',
                detail_days=7, summary_days=35, sample_interval_seconds=10,
                tls_cert_file='', tls_key_file='')


def private_json(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    temp = path.with_name(path.name + '.new')
    temp.write_text(json.dumps(value, ensure_ascii=False, indent=2) + '\n')
    temp.chmod(0o600)
    temp.replace(path)


def layout():
    os.umask(0o077)
    for name in ('state', 'panel', 'monitor', 'logs', 'core', 'certs', 'tmp'):
        (DATA / name).mkdir(parents=True, exist_ok=True)


def validate(config):
    import ipaddress
    ipaddress.ip_address(config['bind'])
    ports = [config[k] for k in ('port', 'panel_port', 'xray_api_port', 'metrics_port')]
    if any(type(p) is not int or not 1 <= p <= 65535 for p in ports) or len(set(ports)) != 4:
        raise ValueError('Four listener ports must be distinct integers in 1..65535')
    if not 1 <= config['detail_days'] <= 7 or not config['detail_days'] <= config['summary_days'] <= 35:
        raise ValueError('Retention requires 1 <= detail <= 7 and detail <= summary <= 35')
    if not 1 <= config['sample_interval_seconds'] <= 300:
        raise ValueError('Sampling interval must be 1..300 seconds')
    base = config['base_path']
    if not isinstance(base, str) or not re.fullmatch(r'/[A-Za-z0-9_/-]*', base) or '//' in base:
        raise ValueError('Base path must start with / and contain only letters, digits, _, - and /')
    config['base_path'] = '/' + base.strip('/') + '/' if base.strip('/') else '/'
    cert, key = config['tls_cert_file'], config['tls_key_file']
    if bool(cert) != bool(key):
        raise ValueError('TLS certificate and key must be set together')
    if cert:
        for value in (cert, key):
            p = Path(value).resolve()
            if not p.is_relative_to(DATA / 'certs') or not p.is_file():
                raise ValueError('TLS files must be regular files under /data/certs')
        ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER).load_cert_chain(cert, key)


def config():
    result = DEFAULTS | (json.loads(CONFIG.read_text()) if CONFIG.exists() else {})
    result.setdefault('instance_id', str(uuid.uuid4()))
    validate(result)
    return result


def official(*args):
    # Official CLI output may contain tokens or passwords. Do not forward it.
    p = subprocess.run([str(APP / 'x-ui'), 'setting', *args], cwd=APP,
                       capture_output=True, text=True, timeout=60)
    if p.returncode:
        raise RuntimeError('Official settings command failed (output withheld to protect credentials)')
    return p.stdout


def settings():
    # Health checks can run while the first startup is still copying the core.
    # A read must never create an empty database that suppresses initialization.
    with closing(sqlite3.connect(DB.as_uri() + '?mode=ro', uri=True, timeout=15)) as db:
        return dict(db.execute('SELECT key,value FROM settings'))


def put_settings(values):
    with closing(sqlite3.connect(DB, timeout=15)) as db, db:
        for key, value in values.items():
            if db.execute('SELECT 1 FROM settings WHERE key=?', (key,)).fetchone():
                db.execute('UPDATE settings SET value=? WHERE key=?', (value, key))
            else:
                db.execute('INSERT INTO settings(key,value) VALUES(?,?)', (key, value))


def initialize():
    layout()
    cfg = config()
    private_json(CONFIG, cfg)
    marker = DATA / 'core/release.json'
    previous = json.loads(marker.read_text()) if marker.exists() else {}
    if previous.get('panel_sha256') != MANIFEST['panel_sha256']:
        # Keep a core manually updated in the panel until the bundled panel
        # release changes. Config/log/database paths are independent of this.
        shutil.copytree(APP / 'seed/bin', DATA / 'core/bin', dirs_exist_ok=True)
        (DATA / 'core/bin/xray-linux-amd64').chmod(0o755)
        private_json(marker, MANIFEST)
    fresh = not DB.exists()
    if fresh:
        credential_file = DATA / 'state/credentials.json'
        if credential_file.exists():
            credentials = json.loads(credential_file.read_text())
        else:
            credentials = dict(username='monitor-admin', password=secrets.token_urlsafe(24))
            private_json(credential_file, credentials)
        official('-username', credentials['username'], '-password', credentials['password'],
                 '-port', str(cfg['panel_port']), '-listenIP', '127.0.0.1', '-webBasePath', cfg['base_path'])
        put_settings({'subEnable': 'false', 'subJsonEnable': 'false', 'subClashEnable': 'false'})
    else:
        # The monitor is the public entry; keep the official HTTP listener local.
        official('-port', str(cfg['panel_port']), '-listenIP', '127.0.0.1')
    if not TOKEN.exists():
        out = official('-getApiToken', '-tokenName', 'traffic-monitor-docker', '-tokenScope', 'admin')
        match = re.search(r'apiToken:\s*(\S+)', out)
        if not match:
            raise RuntimeError('Official CLI did not return an API token')
        temp = TOKEN.with_suffix('.new')
        temp.write_text(match.group(1) + '\n')
        temp.chmod(0o600)
        temp.replace(TOKEN)
    saved = settings()
    template = json.loads(saved.get('xrayTemplateConfig') or (APP / 'default-xray.json').read_text())
    template.setdefault('log', {})['access'] = '/data/logs/xray-access.log'
    services = template.setdefault('api', {}).setdefault('services', [])
    if 'LoggerService' not in services:
        services.append('LoggerService')
    tag = template['api'].get('tag', 'api')
    api_inbound = next((i for i in template.get('inbounds', []) if i.get('tag') == tag), None)
    if api_inbound is None:
        raise RuntimeError('Persistent Xray template is missing its API inbound; inspect configuration')
    api_inbound.update(listen='127.0.0.1', port=cfg['xray_api_port'])
    if template.get('metrics'):
        template['metrics']['listen'] = '127.0.0.1:' + str(cfg['metrics_port'])
    put_settings({'xrayTemplateConfig': json.dumps(template)})
    write_monitor(cfg)
    return cfg


def panel_address():
    saved = settings()
    # Public TLS is terminated by the monitor. Internal TLS would need a
    # separately trusted upstream certificate and is not configured here.
    if saved.get('webCertFile') or saved.get('webKeyFile'):
        raise RuntimeError('Use x-ui tls to set public HTTPS; clear internal panel certificate settings')
    return 'http://127.0.0.1:' + saved.get('webPort', '12053'), saved.get('webBasePath', '/')


def write_monitor(cfg):
    upstream, base = panel_address()
    target = dict(listen=('[' + cfg['bind'] + ']' if ':' in cfg['bind'] else cfg['bind']) + ':' + str(cfg['port']),
                  upstream=upstream, web_base_path=base, panel_version=MANIFEST['panel_version'],
                  api_token_file=str(TOKEN), db_path='/data/monitor/monitor.db',
                  access_log_path='/data/logs/xray-access.log', web_dir='/app/www', timezone='Asia/Shanghai',
                  sample_interval_seconds=cfg['sample_interval_seconds'], detail_days=cfg['detail_days'],
                  summary_days=cfg['summary_days'], xray_api_address='127.0.0.1:' + str(cfg['xray_api_port']),
                  log_max_bytes=67108864, instance_id=cfg['instance_id'],
                  tls_cert_file=cfg['tls_cert_file'], tls_key_file=cfg['tls_key_file'])
    private_json(DATA / 'state/monitor.json', target)


def api(route, data=None):
    upstream, base = panel_address()
    headers = {'Authorization': 'Bearer ' + TOKEN.read_text().strip(), 'X-Requested-With': 'XMLHttpRequest'}
    if data is not None:
        data = urllib.parse.urlencode(data).encode()
    req = urllib.request.Request(upstream + base + 'panel/api/' + route, data=data, headers=headers)
    with urllib.request.urlopen(req, timeout=5) as response:
        payload = json.load(response)
    if not payload.get('success'):
        raise RuntimeError('Official API request failed')
    return payload.get('obj')


def health():
    cfg = config()
    api('server/status')
    _, base = panel_address()
    host = cfg['bind'] if cfg['bind'] not in ('0.0.0.0', '::') else '127.0.0.1'
    if ':' in host:
        host = '[' + host + ']'
    secure = bool(cfg['tls_cert_file'])
    ctx = ssl.create_default_context(cafile=cfg['tls_cert_file']) if secure else None
    if ctx:
        ctx.check_hostname = False  # Verify the mounted cert, irrespective of its public DNS name.
    try:
        urllib.request.urlopen(('https' if secure else 'http') + '://' + host + ':' + str(cfg['port']) +
                               base + 'traffic-monitor/api/v1/health', timeout=4, context=ctx)
    except urllib.error.HTTPError as e:
        if e.code == 401:
            return
        raise
    raise RuntimeError('Monitor health expected an authenticated endpoint')


def configure():
    layout()
    change = json.load(sys.stdin)
    allowed = set(DEFAULTS) | {'credentials', 'reset_two_factor', 'rotate_token'}
    if set(change) - allowed:
        raise ValueError('Unknown configuration fields')
    cfg = config()
    cfg.update({k: v for k, v in change.items() if k in DEFAULTS})
    validate(cfg)
    if 'credentials' in change:
        creds = change['credentials']
        if not isinstance(creds.get('username'), str) or not creds['username'] or len(creds['username']) > 128:
            raise ValueError('Username must contain 1..128 characters')
        if not isinstance(creds.get('password'), str) or not 12 <= len(creds['password']) <= 256:
            raise ValueError('Password must contain 12..256 characters')
        if DB.exists():
            official('-username', creds['username'], '-password', creds['password'])
        private_json(DATA / 'state/credentials.json', creds)
    if DB.exists():
        if 'base_path' in change:
            official('-webBasePath', cfg['base_path'])
        if change.get('reset_two_factor'):
            official('-resetTwoFactor')
        if change.get('rotate_token'):
            TOKEN.unlink(missing_ok=True)
    private_json(CONFIG, cfg)
    print('Configuration saved; restart the service to apply.')


def stop(proc):
    if proc and proc.poll() is None:
        # Monitor first, then the panel saves counters and stops its core.
        proc.terminate()
        try:
            proc.wait(timeout=15)
        except subprocess.TimeoutExpired:
            os.killpg(proc.pid, signal.SIGTERM)
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(proc.pid, signal.SIGKILL)
                proc.wait()
    if proc:
        try:
            os.killpg(proc.pid, signal.SIGTERM)
        except ProcessLookupError:
            pass


def serve():
    stopping = False
    def shutdown(*_):
        nonlocal stopping
        stopping = True
    signal.signal(signal.SIGTERM, shutdown)
    signal.signal(signal.SIGINT, shutdown)
    panel = monitor = None
    try:
        cfg = initialize()
        panel = subprocess.Popen([str(APP / 'x-ui'), 'run'], cwd=APP, start_new_session=True)
        for _ in range(100):
            if stopping:
                return
            if panel.poll() is not None:
                raise RuntimeError('Official panel exited during startup')
            try:
                api('server/status')
                break
            except (OSError, ValueError, RuntimeError):
                time.sleep(.5)
        else:
            raise RuntimeError('Official panel startup timed out; check its configuration')
        def start_monitor():
            write_monitor(cfg)
            return subprocess.Popen([str(APP / 'traffic-monitor'), '-config', '/data/state/monitor.json'],
                                    cwd=APP, start_new_session=True)
        monitor = start_monitor()
        address = panel_address()
        print('Services started. Initial credentials: data/state/credentials.json', flush=True)
        tick = 0
        while not stopping:
            if panel.poll() is not None or monitor.poll() is not None:
                raise RuntimeError('A supervised service exited; restarting the container is required')
            time.sleep(1)
            tick += 1
            if tick % 5 == 0:
                new_address = panel_address()
                if new_address != address:
                    # Panel GUI base-path changes are also reflected in the monitor.
                    stop(monitor)
                    monitor = start_monitor()
                    address = new_address
                    print('Monitor synchronized with official panel address.', flush=True)
    finally:
        stop(monitor)
        stop(panel)
        # As PID 1, reap any orphaned descendants after graceful service exit.
        try:
            while os.waitpid(-1, os.WNOHANG)[0] > 0:
                pass
        except ChildProcessError:
            pass


def main():
    action = sys.argv[1] if len(sys.argv) > 1 else 'serve'
    if action == 'serve':
        serve()
    elif action == 'health':
        health()
    elif action == 'configure':
        configure()
    elif action == 'xray-restart':
        api('server/restartXrayService', {})
        print('Xray restarted.')
    elif action == 'settings':
        cfg = config()
        _, cfg['base_path'] = panel_address()
        print(json.dumps(cfg | {'panel_version': MANIFEST['panel_version']}, ensure_ascii=False))
    else:
        raise ValueError('Unknown container action')


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        # Do not echo exceptions containing requests, command arguments or credentials.
        print('Container operation failed: ' + (str(error) if isinstance(error, (ValueError, RuntimeError)) else type(error).__name__), file=sys.stderr)
        sys.exit(1)
