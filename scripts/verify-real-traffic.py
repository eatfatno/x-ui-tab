#!/usr/bin/env python3
"""Check the latest two real fixture clients against stored monitor history."""
import json
import sqlite3
import lab

clients = [c for c in lab.api('clients/list') if c['email'] in ('monitor-test-alice', 'monitor-test-bob')]
assert len(clients) == 2, 'Run test-real-traffic.py first'
config = json.loads((lab.MONITOR / 'config.json').read_text())
with sqlite3.connect('file:' + str(lab.ROOT / 'data/monitor/monitor.db') + '?mode=ro', uri=True) as db:
    report = []
    for client in clients:
        identity = config['instance_id'] + ':' + str(client['id']) + ':' + str(client['createdAt'])
        visits = db.execute('select target,port,protocol,inbound from visits where client_id=? order by id', (identity,)).fetchall()
        up, down = db.execute('select coalesce(sum(up),0),coalesce(sum(down),0) from traffic where client_id=?', (identity,)).fetchone()
        assert len(visits) == 4, 'Each fixture client must have exactly four accesses'
        assert {v[0] for v in visits} == {'localhost', '127.0.0.1'}
        assert {v[1] for v in visits} == {19090, 19091}
        assert all(v[2] == 'tcp' and v[3] == 'monitor-test-inbound' for v in visits)
        assert up == client['traffic']['up'] and down == client['traffic']['down'], 'Monitor sampling must match settled official counters'
        report.append({'email': client['email'], 'visits': len(visits), 'sampled_up': up, 'sampled_down': down,
                       'equals_panel_counters': True, 'matched_identity': True})
    assert not db.execute("select 1 from visits where inbound='api'").fetchone()
result = {'passed': True, 'client_results': report, 'internal_api_excluded': True}
(lab.ROOT / 'artifacts/reports/real-traffic-monitor.json').write_text(json.dumps(result, indent=2) + '\n')
print(json.dumps(result))
