#!/usr/bin/env python3
"""Workspace-confined WSL lifecycle/provisioning for the official panel and monitor."""
import argparse
import json
import os
from pathlib import Path
import re
import secrets
import signal
import subprocess
import time
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
PANEL = ROOT / 'runtime/panel'
MONITOR = ROOT / 'runtime/monitor'
LOGS = ROOT / 'runtime/logs'
STATE = ROOT / 'runtime/lab-state.json'

def env():
    result = os.environ.copy()
    result.update(XUI_BIN_FOLDER=str(PANEL / 'bin'), XUI_DB_FOLDER=str(ROOT / 'data/panel'),
                  XUI_LOG_FOLDER=str(LOGS), XUI_LOG_LEVEL='warning',
                  XUI_MAIN_FOLDER=str(PANEL), XUI_SKIP_HSTS='true', TZ='Asia/Shanghai')
    return result

def write_private(path, obj):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(obj, indent=2) + '\n', encoding='utf8')
    path.chmod(0o600)

def state():
    return json.loads(STATE.read_text())

def api(path, data=None, method=None):
    s = state()
    token = (MONITOR / 'api-token').read_text().strip()
    headers = {'Authorization': 'Bearer ' + token, 'X-Requested-With': 'XMLHttpRequest'}
    if data is not None:
        data = urllib.parse.urlencode(data).encode()
        headers['Content-Type'] = 'application/x-www-form-urlencoded'
    req = urllib.request.Request(s['upstream'] + s['base_path'] + 'panel/api/' + path,
                                 data=data, headers=headers, method=method)
    with urllib.request.urlopen(req, timeout=30) as response:
        payload = json.load(response)
    if not payload.get('success'):
        raise RuntimeError('Panel API operation failed: ' + str(payload.get('msg', 'unknown')))
    return payload.get('obj')

def process_matches(name, pid):
    try:
        expected = PANEL / 'x-ui' if name == 'panel' else MONITOR / 'traffic-monitor'
        exe = Path('/proc') / str(pid) / 'exe'
        return str(exe.resolve()).removesuffix(' (deleted)') == str(expected)
    except OSError:
        return False

def start_one(name):
    pidfile = ROOT / 'runtime' / (name + '.pid')
    if pidfile.exists() and process_matches(name, int(pidfile.read_text())):
        print(name, 'already running')
        return
    if name == 'panel':
        cmd, cwd = [str(PANEL / 'x-ui'), 'run'], PANEL
    else:
        cmd, cwd = [str(MONITOR / 'traffic-monitor'), '-config', str(MONITOR / 'config.json')], MONITOR
    LOGS.mkdir(parents=True, exist_ok=True)
    with (LOGS / (name + '.log')).open('ab') as output:
        p = subprocess.Popen(cmd, cwd=cwd, env=env(), stdin=subprocess.DEVNULL,
                             stdout=output, stderr=subprocess.STDOUT, start_new_session=True)
    pidfile.write_text(str(p.pid))
    time.sleep(0.7)
    if p.poll() is not None:
        raise RuntimeError(name + ' failed; inspect workspace runtime/logs/' + name + '.log')
    print(name, 'started, pid', p.pid)

def stop_one(name):
    pidfile = ROOT / 'runtime' / (name + '.pid')
    if not pidfile.exists():
        return
    pid = int(pidfile.read_text())
    if process_matches(name, pid):
        os.kill(pid, signal.SIGTERM)
        for _ in range(100):
            if not process_matches(name, pid):
                break
            time.sleep(.1)
        else:
            raise RuntimeError(name + ' did not exit gracefully; no forced kill performed')
    pidfile.unlink(missing_ok=True)
    print(name, 'stopped')

def wait_panel():
    for _ in range(60):
        try:
            api('server/status')
            return
        except Exception:
            time.sleep(.5)
    raise RuntimeError('Panel did not become ready')

def configure_log():
    raw = api('xray/', {}, 'POST')
    wrapper = json.loads(raw) if isinstance(raw, str) else raw
    template = wrapper['xraySetting']
    if isinstance(template, str):
        template = json.loads(template)
    desired = str(LOGS / 'xray-access.log')
    services = template.setdefault('api', {}).setdefault('services', [])
    changed = template.setdefault('log', {}).get('access') != desired
    template['log']['access'] = desired
    if 'LoggerService' not in services:
        services.append('LoggerService')
        changed = True
    if changed:
        api('xray/update', {'xraySetting': json.dumps(template)})
    print('Persistent Xray access log configured; existing routing and sniffing preserved.')

def init():
    if STATE.exists():
        print('Lab already initialized; preserving credentials and databases.')
        return
    if not (PANEL / 'x-ui').exists():
        raise RuntimeError('Fetch and extract verified official release first')
    username, password = 'monitor-admin', secrets.token_urlsafe(24)
    base_path = '/'
    output = subprocess.run([str(PANEL / 'x-ui'), 'setting', '-port', '12053', '-listenIP', '127.0.0.1',
                             '-webBasePath', base_path, '-username', username, '-password', password],
                            cwd=PANEL, env=env(), capture_output=True, text=True, check=True)
    # CLI output can contain sensitive data: never forward it to the terminal.
    token_output = subprocess.run([str(PANEL / 'x-ui'), 'setting', '-getApiToken',
                                    '-tokenName', 'traffic-monitor', '-tokenScope', 'admin'],
                                   cwd=PANEL, env=env(), capture_output=True, text=True, check=True)
    found = re.search(r'apiToken:\s*(\S+)', token_output.stdout)
    if not found:
        raise RuntimeError('Official CLI did not return a token')
    tokenfile = MONITOR / 'api-token'
    tokenfile.write_text(found.group(1) + '\n')
    tokenfile.chmod(0o600)
    write_private(STATE, {'username': username, 'password': password, 'base_path': base_path,
                           'upstream': 'http://127.0.0.1:12053', 'public_url': 'http://localhost:18080',
                           'panel_version': '3.9.0'})
    write_private(MONITOR / 'config.json', {
        'listen': '127.0.0.1:18080', 'upstream': 'http://127.0.0.1:12053', 'web_base_path': base_path,
        'panel_version': '3.9.0', 'api_token_file': str(tokenfile),
        'db_path': str(ROOT / 'data/monitor/monitor.db'), 'access_log_path': str(LOGS / 'xray-access.log'),
        'web_dir': str(MONITOR / 'www'), 'timezone': 'Asia/Shanghai',
        'sample_interval_seconds': 10, 'detail_days': 7, 'summary_days': 35,
        'xray_api_address': '127.0.0.1:62789', 'log_max_bytes': 67108864, 'instance_id': 'wsl-local'})
    start_one('panel')
    wait_panel()
    configure_log()
    # This is an isolated lab: disable the otherwise externally listening subscription server.
    settings = api('setting/all', {}, 'POST')
    settings['subEnable'] = False
    settings['subJsonEnable'] = False
    settings['subClashEnable'] = False
    api('setting/update', {k: json.dumps(v) if isinstance(v, (bool, dict, list)) else v for k, v in settings.items()})
    stop_one('panel')
    start_one('panel')
    wait_panel()
    print('Initialized. Login credentials are stored only in runtime/lab-state.json.')

def status():
    for name in ('panel', 'monitor'):
        pf = ROOT / 'runtime' / (name + '.pid')
        alive = pf.exists() and process_matches(name, int(pf.read_text()))
        print(f'{name}: {"running" if alive else "stopped"}')
    if STATE.exists():
        print('Browser:', state()['public_url'] + state()['base_path'] + 'panel/?monitor=traffic')

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('action', choices=['init', 'start', 'stop', 'restart', 'status', 'configure-log'])
    parser.add_argument('--only', choices=['panel', 'monitor'])
    args = parser.parse_args()
    if args.action == 'init':
        init()
    elif args.action == 'configure-log':
        configure_log()
    elif args.action == 'status':
        status()
    else:
        names = [args.only] if args.only else ['panel', 'monitor']
        if args.action in ('stop', 'restart'):
            for name in reversed(names):
                stop_one(name)
        if args.action in ('start', 'restart'):
            for name in names:
                start_one(name)

if __name__ == '__main__':
    main()
