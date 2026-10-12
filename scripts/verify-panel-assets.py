#!/usr/bin/env python3
"""Read-only checks for official module bytes, failure caching and deployed state."""
import hashlib
import json
from pathlib import Path
import runpy
import sqlite3
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]


def get(origin, route):
    req = urllib.request.Request(origin+route, headers={'Accept-Encoding':'identity'})
    try:
        response = urllib.request.urlopen(req, timeout=10)
    except urllib.error.HTTPError as failure:
        response = failure
    with response:
        return response.status, response.headers, response.read()


def main():
    lab = runpy.run_path(str(ROOT/'scripts/lab.py'))
    Browser = runpy.run_path(str(ROOT/'scripts/verify-runtime.py'))['Browser']
    state = lab['state']()
    config = json.loads((ROOT/'runtime/monitor/config.json').read_text())
    base, origin = state['base_path'], state['public_url'].rstrip('/')
    route = base+'assets/ClientQrModal-Bt-NPyL_.js'
    upstream = get(state['upstream'], route)
    proxied = get(origin, route)
    assert upstream[0] == proxied[0] == 200
    assert upstream[2] == proxied[2]
    assert upstream[1]['Cache-Control'] == proxied[1]['Cache-Control']
    missing = get(origin, base+'assets/traffic-monitor-missing-module-probe.js')
    assert missing[0] == 404 and missing[1]['Cache-Control'] == 'no-store'
    recovery = get(origin, base+'traffic-monitor/panel-recovery.js')
    assert recovery[0] == 200 and recovery[1]['Cache-Control'] == 'no-cache'
    assert recovery[2] == (ROOT/'apps/traffic-monitor/web/public/panel-recovery.js').read_bytes()
    browser = Browser(origin)
    csrf = browser.obj(base+'csrf-token')
    browser.obj(base+'login', {'username':state['username'],'password':state['password']}, {'X-CSRF-Token':csrf})
    health = browser.obj(base+'traffic-monitor/api/v1/health')
    clients = browser.obj(base+'traffic-monitor/api/v1/clients')
    with sqlite3.connect('file:'+config['db_path']+'?mode=ro', uri=True) as db:
        integrity = db.execute('PRAGMA integrity_check').fetchone()[0]
        visits = dict(db.execute('SELECT email,COUNT(*) FROM visits GROUP BY email'))
        deletions = db.execute('SELECT COUNT(*) FROM history_deletions').fetchone()[0]
    sha = lambda p: hashlib.sha256((ROOT/p).read_bytes()).hexdigest()
    report = {'passed':True,'health':health['status'],'issues':health['issues'],'integrity':integrity,
        'visits_by_email':visits,'history_deletions':deletions,
        'current_client_count':sum(not c['deleted'] for c in clients),
        'visible_history_client_count':sum(c['deleted'] for c in clients),
        'temporary_official_clients_removed':not any(c['email'].startswith('panel-ui-fixture-') for c in lab['api']('clients/list')),
        'temporary_official_inbounds_removed':not any(c['tag'].startswith('panel-ui-fixture-') for c in lab['api']('inbounds/list')),
        'build_matches_runtime':sha('artifacts/build/traffic-monitor') == sha('runtime/monitor/traffic-monitor'),
        'recovery_script_matches_runtime':True,'detail_days':config['detail_days'],'summary_days':config['summary_days'],
        'official_qr_module_byte_identical':True,'failed_asset_cache_control':missing[1]['Cache-Control'],
        'recovery_cache_control':recovery[1]['Cache-Control']}
    assert report['health'] == 'ok' and not report['issues'] and integrity == 'ok'
    assert all(report[k] for k in ('temporary_official_clients_removed','temporary_official_inbounds_removed','build_matches_runtime'))
    (ROOT/'artifacts/reports/panel-proxy-final.json').write_text(json.dumps(report,indent=2)+'\n')
    print(json.dumps(report,indent=2))


if __name__ == '__main__':
    main()
