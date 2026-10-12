#!/usr/bin/env python3
"""Run destructive browser checks against a disposable, isolated monitor DB.
Uses real official-panel session validation; never deletes live monitor history.
"""
from datetime import datetime, timedelta, timezone
import json
import os
from pathlib import Path
import re
import sqlite3
import subprocess
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]

def main():
    folder = ROOT / ('artifacts/fixtures/enhancements-' + str(time.time_ns()))
    folder.mkdir(parents=True)
    config = json.loads((ROOT / 'runtime/monitor/config.json').read_text())
    config.update(listen='127.0.0.1:18082', db_path=str(folder / 'monitor.db'), access_log_path=str(folder / 'access.log'), instance_id='enhancement-fixture', xray_api_address='')
    (folder / 'access.log').touch()
    (folder / 'config.json').write_text(json.dumps(config))
    schema = re.search(r'const schema = `([\s\S]*?)`', (ROOT / 'apps/traffic-monitor/internal/monitor/store.go').read_text()).group(1)
    now = datetime.now(timezone.utc)
    start = now - timedelta(days=2)
    # Use fixed historical minutes so the live collector cannot change this preview.
    at = int((now - timedelta(minutes=2)).replace(second=0, microsecond=0).timestamp() * 1000)
    clients = [('fixture-alice', 'fixture-alice'), ('fixture-bob', 'fixture-bob')]
    with sqlite3.connect(config['db_path']) as db:
        db.executescript(schema)
        db.execute("INSERT INTO meta VALUES('started_at',?)", (start.isoformat().replace('+00:00', 'Z'),))
        for client_id, email in clients:
            db.execute('INSERT INTO clients VALUES(?,?,?,0,?,?,?,?)', (client_id,email,'[1]',100000,200000,at,1))
            db.execute('INSERT INTO aliases VALUES(?,?,?,NULL)', (client_id,email, int(start.timestamp()*1000)))
            db.execute('INSERT INTO traffic VALUES(?,?,?,?,?)', (at,client_id,'[1]',1048576,2097152))
        # Empty retired identities must not appear merely because old panel
        # counters or an email also used by another identity are still stored.
        db.execute('INSERT INTO clients VALUES(?,?,?,1,?,?,?,1)', ('fixture-alice-removed','fixture-alice','[1]',123,456,at))
        targets = [('localhost',19090),('localhost',19091),('localhost',19090),('www.baidu.com',443),('bdstatic.com',443),('github.com',443),('avatars.githubusercontent.com',443),('youtube.com',443),('r1.googlevideo.com',443),('douyin.com',443),('video.douyinvod.com',443),('203.0.113.7',443)]
        targets += [(host,443) for host in ['youtubei.googleapis.com','api.telegram.org','149.154.167.51','2001:b28:f23d::1','x.com','api.twitter.com','video.twimg.com','chatgpt.com','ios.chat.openai.com','cdn.oaistatic.com','www.taobao.com','acs.m.taobao.com','adash.m.taobao.com']]
        for client_id,email in clients:
            for i,(target,port) in enumerate(targets):
                db.execute('INSERT INTO visits(time,client_id,email,target,port,protocol,inbound,inbound_id,outbound,status) VALUES(?,?,?,?,?,?,?,?,?,?)', (at+i*1000,client_id,email,target,port,'tcp','fixture-inbound',1,'direct','accepted'))
            db.execute('INSERT INTO visit_counts VALUES(?,?,?,?)',(at,client_id,1,len(targets)))
    minute = datetime.fromtimestamp(at/1000, timezone(timedelta(hours=8)))
    report = {'fixture': str(folder.relative_to(ROOT)), 'url': 'http://127.0.0.1:18082', 'expected_groups_per_client':10, 'expected_visits_per_client':len(targets), 'expected_bytes_per_client':3145728, 'minute':minute.strftime('%Y-%m-%d %H:%M'), 'empty_minute':(minute-timedelta(minutes=1)).strftime('%Y-%m-%d %H:%M'), 'minute_start':datetime.fromtimestamp(at/1000,timezone.utc).isoformat(), 'minute_end':datetime.fromtimestamp(at/1000+60,timezone.utc).isoformat()}
    (folder / 'fixture.json').write_text(json.dumps(report))
    with (folder / 'monitor.log').open('wb') as log:
        process = subprocess.Popen([str(ROOT/'artifacts/build/traffic-monitor'),'-config',str(folder/'config.json')],cwd=folder,env=os.environ.copy(),stdin=subprocess.DEVNULL,stdout=log,stderr=log)
        try:
            for _ in range(80):
                if process.poll() is not None:
                    raise RuntimeError('Isolated monitor failed to start')
                try:
                    urllib.request.urlopen('http://127.0.0.1:18082/traffic-monitor/api/v1/health',timeout=2)
                except urllib.error.HTTPError as e:
                    if e.code==401: break
                except OSError: pass
                time.sleep(.1)
            else: raise RuntimeError('Fixture monitor readiness timeout')
            result = subprocess.run(['node',str(ROOT/'scripts/browser-tests/enhancements.mjs')],cwd=ROOT,env={**os.environ,'ENHANCEMENT_FIXTURE':str(folder/'fixture.json')},timeout=180)
            if result.returncode: raise RuntimeError('Enhancement browser checks failed')
        finally:
            process.terminate()
            try: process.wait(timeout=15)
            except subprocess.TimeoutExpired: process.kill();process.wait()

if __name__=='__main__': main()
