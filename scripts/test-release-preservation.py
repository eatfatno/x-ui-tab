#!/usr/bin/env python3
"""Exercise replacement with the same official release against the live WSL lab.

The cross-version migration test lives separately in test-official-upgrade.py.
"""
import hashlib
import json
from pathlib import Path
import sqlite3
import subprocess
import sys
import time

import lab

def fingerprints():
    files = ['runtime/monitor/traffic-monitor', 'runtime/monitor/config.json',
             'runtime/monitor/api-token', 'runtime/monitor/www/adapter.js']
    result = {}
    for name in files:
        with (lab.ROOT / name).open('rb') as f:
            result[name] = hashlib.file_digest(f, 'sha256').hexdigest()
    return result

def snapshot():
    with sqlite3.connect('file:' + str(lab.ROOT / 'data/monitor/monitor.db') + '?mode=ro', uri=True) as db:
        visits = list(db.execute('select * from visits order by id'))
        traffic = list(db.execute('select time,client_id,inbound_ids,up,down from traffic where up>0 or down>0 order by time,client_id,inbound_ids'))
        return visits, traffic, dict(db.execute('select key,value from meta'))

before_files = fingerprints()
before_visits, before_traffic, before_meta = snapshot()
monitor_pid = (lab.ROOT / 'runtime/monitor.pid').read_text().strip()
version = 'v' + lab.state()['panel_version'].lstrip('v')
subprocess.run([sys.executable, str(lab.ROOT / 'scripts/upgrade-panel.py'), version], check=True)
time.sleep(13)
after_visits, after_traffic, after_meta = snapshot()
assert before_files == fingerprints(), 'Monitor program/config/assets/token changed'
assert monitor_pid == (lab.ROOT / 'runtime/monitor.pid').read_text().strip(), 'Monitor process restarted unexpectedly'
assert all(row in after_visits for row in before_visits), 'Existing access details changed'
after_buckets = {(r[0], r[1], r[2]): (r[3], r[4]) for r in after_traffic}
for row in before_traffic:
    up, down = after_buckets[(row[0], row[1], row[2])]
    assert up >= row[3] and down >= row[4], 'Traffic history decreased'
assert before_meta['started_at'] == after_meta['started_at'], 'Monitoring origin changed'
assert after_meta['last_sample_at'] > before_meta['last_sample_at'], 'Collection did not resume'
result = {'passed': True, 'official_version': version, 'operation': 'same-version official release replacement',
          'monitor_process_preserved': True, 'monitor_files_preserved': True,
          'historical_visits_preserved': len(before_visits), 'historical_nonzero_buckets_preserved': len(before_traffic),
          'collection_resumed': True, 'cross_version_test': 'See version-upgrade.json for isolated v3.8.5 to v3.9.0.'}
(lab.ROOT / 'artifacts/reports/release-preservation.json').write_text(json.dumps(result, indent=2) + '\n')
print(json.dumps(result))
