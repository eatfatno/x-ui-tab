#!/usr/bin/env python3
"""Verify a running local lab without changing panel configuration or monitor data.

Run inside WSL after sourcing scripts/env.sh. Credentials are read from the
workspace and never included in terminal output or reports. The only writes are
test browser sessions and disposable artifacts/reports/runtime-sqlite-* databases.
"""
import argparse
import copy
from datetime import datetime, timedelta, timezone
import http.cookiejar
import json
import os
from pathlib import Path
import re
import sqlite3
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from zoneinfo import ZoneInfo

ROOT = Path(__file__).resolve().parents[1]
REPORTS = ROOT / 'artifacts/reports'


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class Browser:
    def __init__(self, origin, jar=None):
        self.origin = origin.rstrip('/')
        self.jar = jar if jar is not None else http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.jar), NoRedirect())

    def request(self, path, data=None, headers=None, method=None):
        values = {'Accept': 'application/json', 'X-Requested-With': 'XMLHttpRequest'}
        values.update(headers or {})
        if data is not None:
            data = urllib.parse.urlencode(data).encode()
            values['Content-Type'] = 'application/x-www-form-urlencoded'
        request = urllib.request.Request(self.origin + path, data=data, headers=values, method=method)
        try:
            response = self.opener.open(request, timeout=15)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            body = response.read(16 * 1024 * 1024)
            return response.code, response.headers, body

    def obj(self, path, data=None, headers=None, method=None):
        code, response_headers, body = self.request(path, data, headers, method)
        assert code == 200, f'Expected HTTP 200, received {code}'
        assert 'application/json' in response_headers.get('Content-Type', ''), 'Expected JSON response'
        payload = json.loads(body)
        assert payload.get('success') is True, 'API returned success=false'
        return payload.get('obj')


def require_keys(value, keys):
    assert isinstance(value, dict), 'Expected an object'
    assert set(keys) <= value.keys(), 'Missing required response fields'


def crash_writer(path):
    db = sqlite3.connect(path)
    db.execute('PRAGMA cache_size=16')
    db.execute('BEGIN IMMEDIATE')
    db.execute("UPDATE durable SET payload=zeroblob(8192), marker='uncommitted'")
    # Deliberately bypass cleanup, allowing SQLite hot-journal recovery to run.
    os._exit(17)


def verify_mounted_sqlite():
    scratch = REPORTS / ('runtime-sqlite-' + str(time.time_ns()))
    scratch.mkdir(parents=True)
    dbpath = scratch / 'concurrency.db'
    db = sqlite3.connect(dbpath)
    assert db.execute('PRAGMA journal_mode=DELETE').fetchone()[0] == 'delete'
    db.execute('PRAGMA synchronous=FULL')
    db.executescript('CREATE TABLE counter(id INTEGER PRIMARY KEY, value INTEGER NOT NULL); INSERT INTO counter VALUES(1,0);')
    db.commit()
    db.close()
    failures = []
    read_counts = [0, 0]
    finished = threading.Event()
    ready = threading.Barrier(3)

    def reader(index):
        try:
            conn = sqlite3.connect(dbpath, timeout=10)
            previous = -1
            ready.wait(timeout=5)
            while not finished.is_set():
                value = conn.execute('SELECT value FROM counter WHERE id=1').fetchone()[0]
                assert value >= previous, 'Reader observed a committed counter move backwards'
                previous = value
                read_counts[index] += 1
                time.sleep(.001)
            conn.close()
        except Exception as error:
            failures.append(type(error).__name__)

    threads = [threading.Thread(target=reader, args=(index,)) for index in range(2)]
    for thread in threads:
        thread.start()
    ready.wait(timeout=5)
    conn = sqlite3.connect(dbpath, timeout=10)
    try:
        for _ in range(100):
            with conn:
                conn.execute('UPDATE counter SET value=value+1 WHERE id=1')
            time.sleep(.001)
    finally:
        finished.set()
        for thread in threads:
            thread.join(timeout=15)
    assert not failures and all(read_counts), 'Concurrent readers failed or made no progress'
    assert conn.execute('SELECT value FROM counter').fetchone()[0] == 100
    assert conn.execute('PRAGMA integrity_check').fetchone()[0] == 'ok'
    conn.execute('CREATE TABLE durable(id INTEGER PRIMARY KEY, payload BLOB, marker TEXT)')
    conn.executemany('INSERT INTO durable VALUES(?,zeroblob(8192),?)', [(i, 'committed') for i in range(512)])
    conn.commit()
    conn.close()
    child = subprocess.run([sys.executable, str(Path(__file__).resolve()), '--crash-writer', str(dbpath)],
                           cwd=ROOT, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=30)
    assert child.returncode == 17, 'Crash writer did not reach the intended checkpoint'
    with sqlite3.connect(dbpath, timeout=10) as recovered:
        assert recovered.execute('PRAGMA integrity_check').fetchone()[0] == 'ok'
        assert recovered.execute("SELECT count(*) FROM durable WHERE marker='committed'").fetchone()[0] == 512
        assert recovered.execute("SELECT count(*) FROM durable WHERE marker='uncommitted'").fetchone()[0] == 0
    return {'committed_writes': 100, 'reader_iterations': read_counts,
            'crash_rollback_rows': 512, 'scratch_directory': str(scratch.relative_to(ROOT)),
            'note': 'SQLite filesystem behavior test using Python sqlite3; application driver covered separately by Go tests.'}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--crash-writer', help=argparse.SUPPRESS)
    args = parser.parse_args()
    if args.crash_writer:
        candidate = Path(args.crash_writer).resolve()
        assert candidate.is_relative_to(REPORTS.resolve()), 'Crash test path outside project artifacts'
        crash_writer(candidate)
    state = json.loads((ROOT / 'runtime/lab-state.json').read_text())
    config = json.loads((ROOT / 'runtime/monitor/config.json').read_text())
    token = Path(config['api_token_file']).read_text().strip()
    base = '/' + state['base_path'].strip('/')
    if base != '/':
        base += '/'
    prefix = base + 'traffic-monitor/'
    origin = state['public_url']
    # Use the WSL loopback path; Windows localhost remains the user-facing URL.
    origin_parts = urllib.parse.urlsplit(origin)
    if origin_parts.hostname == 'localhost':
        origin = urllib.parse.urlunsplit((origin_parts.scheme, '127.0.0.1:' + str(origin_parts.port), '', '', ''))
    browser = Browser(origin)
    anonymous = Browser(origin)
    checks = []
    REPORTS.mkdir(parents=True, exist_ok=True)

    def check(name, operation):
        try:
            evidence = operation()
            checks.append({'name': name, 'status': 'passed', 'evidence': evidence})
            print('PASS', name)
            return evidence
        except Exception as error:
            # Exception messages can embed request URLs or server data; retain type only.
            checks.append({'name': name, 'status': 'failed', 'error_type': type(error).__name__})
            print('FAIL', name, type(error).__name__)
            return None

    def denied_without_cookie():
        codes = []
        attempts = ({}, {'Authorization': 'Bearer forged-monitor-test'}, {'Authorization': 'Bearer ' + token},
                    {'Cookie': '3x-ui=invalid-test-session', 'Authorization': 'Bearer ' + token},
                    {'Cookie': 'other=present', 'Authorization': 'Bearer forged-monitor-test', 'X-Forwarded-User': 'admin'})
        for headers in attempts:
            code, _, _ = anonymous.request(prefix + 'api/v1/summary', headers=headers)
            assert code == 401, 'Anonymous API read was not rejected'
            codes.append(code)
        code, headers, _ = anonymous.request(prefix)
        assert code in (302, 303, 307) and headers.get('Location') == base, 'Anonymous page did not return to login'
        return {'anonymous_forged_and_real_collector_bearer_status': codes,
                'includes_invalid_cookie_with_valid_collector_token': True, 'page_status': code}

    check('anonymous-cannot-borrow-collector-auth', denied_without_cookie)

    def login():
        csrf = browser.obj(base + 'csrf-token')
        browser.obj(base + 'login', {'username': state['username'], 'password': state['password']}, {'X-CSRF-Token': csrf})
        assert any(cookie.name == '3x-ui' for cookie in browser.jar), 'Panel session cookie missing'
        browser.obj(base + 'panel/api/server/status')
        require_keys(browser.obj(prefix + 'api/v1/health'), ['status', 'last_sample_at'])
        return {'panel_and_monitor_share_session': True}

    logged_in = check('csrf-login-shared-session', login) is not None

    if logged_in:
        def contracts():
            values = {route: browser.obj(prefix + 'api/v1/' + route)
                      for route in ('clients', 'summary', 'traffic', 'visits', 'health')}
            assert isinstance(values['clients'], list) and isinstance(values['traffic'], list)
            for item in values['clients']:
                require_keys(item, ['id', 'email', 'inbound_ids', 'deleted', 'up', 'down', 'last_seen'])
            for item in values['traffic']:
                require_keys(item, ['time', 'up', 'down'])
            summary = values['summary']
            require_keys(summary, ['up', 'down', 'total', 'visit_count', 'panel_up', 'panel_down', 'quality',
                                   'monitor_started_at', 'detail_available_from', 'summary_available_from', 'timezone'])
            assert summary['up'] >= 0 and summary['down'] >= 0 and summary['total'] == summary['up'] + summary['down']
            require_keys(values['visits'], ['items', 'total', 'page', 'page_size', 'expired'])
            for item in values['visits']['items']:
                require_keys(item, ['id', 'time', 'client_id', 'email', 'target', 'port', 'protocol', 'inbound', 'outbound', 'status'])
            health = values['health']
            require_keys(health, ['status', 'panel_version', 'adapter_compatible', 'last_sample_at', 'last_log_at', 'detail_days', 'summary_days', 'issues'])
            assert health['detail_days'] == 7 and health['summary_days'] == 35
            assert health['timezone'] == 'Asia/Shanghai'
            assert health['last_sample_at'], 'Collector has not sampled successfully'
            return {'routes': list(values), 'client_count': len(values['clients']),
                    'visit_count': values['visits']['total'], 'retention': [health['detail_days'], health['summary_days']],
                    'health_status': health['status'], 'health_issues': health['issues']}

        check('authenticated-api-contracts', contracts)

        def query_validation():
            invalid = ['page_size=201', 'page=0', 'inbound_id=-1', 'start=invalid',
                       'start=2026-01-01T00:00:00Z&end=2026-03-01T00:00:00Z']
            for query in invalid:
                code, _, _ = browser.request(prefix + 'api/v1/visits?' + query)
                assert code == 400, 'Invalid query was not rejected'
            code, _, _ = browser.request(prefix + 'api/v1/summary', {}, method='POST')
            assert code == 405, 'Monitor API accepted a write method'
            now = datetime.now(timezone.utc)
            query = urllib.parse.urlencode({'start': (now - timedelta(days=40)).isoformat(), 'end': (now - timedelta(days=39)).isoformat()})
            old = browser.obj(prefix + 'api/v1/summary?' + query)
            assert 'summary_expired' in old['quality'] and 'detail_expired' in old['quality']
            assert browser.obj(prefix + 'api/v1/visits?' + query)['expired'] is True
            return {'invalid_queries_rejected': len(invalid), 'write_method_status': code, 'expired_window_is_explicit': True}

        check('queries-retention-status-and-read-only-api', query_validation)

        def security_headers():
            code, headers, body = browser.request(prefix)
            assert code == 200 and b'<html' in body.lower()
            assert headers.get('Cache-Control') == 'no-store'
            assert headers.get('X-Frame-Options') == 'SAMEORIGIN'
            assert "frame-ancestors 'self'" in headers.get('Content-Security-Policy', '')
            assert token.encode() not in body and state['password'].encode() not in body
            _, upstream_headers, _ = Browser(state['upstream']).request(base)
            _, proxied_headers, _ = anonymous.request(base)
            for name in ('Content-Security-Policy', 'X-Content-Type-Options', 'X-Frame-Options'):
                expected = re.sub(r"'nonce-[^']+'", "'nonce-per-response'", upstream_headers.get(name, ''))
                observed = re.sub(r"'nonce-[^']+'", "'nonce-per-response'", proxied_headers.get(name, ''))
                assert expected == observed, 'Official security header was changed'
            return {'private_page_no_store': True, 'same_origin_frame_policy': True, 'official_headers_preserved': True}

        check('response-security-and-secret-exclusion', security_headers)

        def template_read():
            csrf = browser.obj(base + 'csrf-token')
            raw = browser.obj(base + 'panel/api/xray/', {}, {'X-CSRF-Token': csrf})
            wrapper = json.loads(raw) if isinstance(raw, str) else raw
            template = wrapper['xraySetting']
            template = json.loads(template) if isinstance(template, str) else template
            rendered = json.loads((ROOT / 'runtime/panel/bin/config.json').read_text())
            assert template['log']['access'] == config['access_log_path']
            assert rendered['log']['access'] == template['log']['access']
            assert 'LoggerService' in template['api']['services']
            assert 'LoggerService' in rendered['api']['services']
            return {'persistent_and_rendered_access_path_match': True, 'logger_service_enabled': True}

        check('persistent-xray-log-config-read-only', template_read)

        def expired_cookie():
            jar = http.cookiejar.CookieJar()
            for cookie in browser.jar:
                expired = copy.copy(cookie)
                expired.expires = int(time.time()) - 1
                jar.set_cookie(expired)
            code, _, _ = Browser(origin, jar).request(prefix + 'api/v1/health')
            assert code == 401
            code, _, _ = anonymous.request(prefix + 'api/v1/health', headers={'Cookie': '3x-ui=invalid-test-session'})
            assert code == 401
            return {'expired_browser_cookie_status': 401, 'tampered_session_status': 401,
                    'limit': 'Browser expiry and invalid session tested; no live server clock or session lifetime was changed.'}

        check('expired-and-tampered-cookie-denied', expired_cookie)

        def logout():
            csrf = browser.obj(base + 'csrf-token')
            browser.obj(base + 'logout', {}, {'X-CSRF-Token': csrf})
            code, _, _ = browser.request(prefix + 'api/v1/health')
            assert code == 401
            return {'post_logout_status': code}

        check('logout-removes-monitor-access', logout)

    def live_database():
        path = Path(config['db_path'])
        with sqlite3.connect(path.as_uri() + '?mode=ro', uri=True, timeout=10) as conn:
            assert conn.execute('PRAGMA journal_mode').fetchone()[0] == 'delete'
            assert conn.execute('PRAGMA integrity_check').fetchone()[0] == 'ok'
        return {'journal_mode': 'delete', 'integrity_check': 'ok', 'opened_read_only': True}

    check('live-monitor-sqlite-integrity', live_database)
    check('mounted-filesystem-concurrency-and-crash-recovery', verify_mounted_sqlite)
    report = {'checked_at': datetime.now(ZoneInfo('Asia/Shanghai')).isoformat(),
              'base_path': base, 'panel_version': state['panel_version'], 'checks': checks,
              'passed': sum(item['status'] == 'passed' for item in checks),
              'failed': sum(item['status'] == 'failed' for item in checks),
              'limitations': ['Non-root base paths and actual upstream authorization revocation require backend tests.',
                              'Real traffic, browser layout and release upgrade are verified separately.']}
    (REPORTS / 'runtime-checks.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    print(f"Report: artifacts/reports/runtime-checks.json; {report['passed']} passed, {report['failed']} failed")
    return 1 if report['failed'] else 0


if __name__ == '__main__':
    sys.exit(main())
