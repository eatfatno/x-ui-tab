#!/usr/bin/env python3
"""Validate the installed Xray LoggerService reopening actual access log files."""
import json
from pathlib import Path
import sqlite3
import time
import lab

cfg_file = lab.MONITOR / 'config.json'
original = cfg_file.read_text()
config = json.loads(original)
access = Path(config['access_log_path'])
before_inode = access.stat().st_ino
before_rotated = set(access.parent.glob(access.name + '.monitor-*'))
def visits():
    with sqlite3.connect('file:' + str(lab.ROOT / 'data/monitor/monitor.db') + '?mode=ro', uri=True) as db:
        return list(db.execute('select * from visits order by id'))
before_visits = visits()
try:
    config['log_max_bytes'] = 4096
    cfg_file.write_text(json.dumps(config, indent=2) + '\n')
    # Real control API calls grow only internal log rows, which must be excluded.
    for _ in range(250):
        if access.stat().st_size >= 4096:
            break
        lab.api('server/status')
        time.sleep(.05)
    if access.stat().st_size < 4096:
        raise RuntimeError('Actual access log not large enough to exercise size rotation')
    lab.stop_one('monitor')
    lab.start_one('monitor')
    for _ in range(100):
        if access.exists() and access.stat().st_ino != before_inode:
            break
        time.sleep(.2)
    else:
        raise AssertionError('Xray did not reopen log after rename')
    rotated = set(access.parent.glob(access.name + '.monitor-*')) - before_rotated
    assert rotated, 'No closed log found'
    time.sleep(12)
    assert access.stat().st_size > 0, 'Reopened log is not receiving Xray output'
    assert visits() == before_visits, 'Rotation duplicated or removed client records'
    report = {'passed': True, 'actual_xray_logger_service': True, 'inode_changed': True,
              'new_log_receives_output': True, 'client_records_unchanged': len(before_visits),
              'closed_log_count': len(rotated)}
    (lab.ROOT / 'artifacts/reports/live-log-rotation.json').write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps(report))
finally:
    cfg_file.write_text(original)
    lab.stop_one('monitor')
    lab.start_one('monitor')
