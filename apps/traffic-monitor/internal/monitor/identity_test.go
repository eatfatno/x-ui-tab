package monitor

import (
	"testing"
	"time"
)

func TestPanelIdentityPrecisionMigrationKeepsHistoryBaselineAndDeletion(t *testing.T) {
	at := time.Date(2026, 10, 10, 4, 0, 0, 0, time.UTC)
	s, cfg := testStore(t, at.Add(-time.Hour))
	old := cfg.InstanceID + ":11:1791604053221"
	current := panelClientID(cfg.InstanceID, "11", 1791604053000)
	if current != panelClientID(cfg.InstanceID, "11", 1791604053221) || current == panelClientID(cfg.InstanceID, "12", 1791604053221) || current == panelClientID(cfg.InstanceID, "11", 1791604054000) {
		t.Fatal("official identity precision or distinct clients lost")
	}
	for i, id := range []string{old, current} {
		_, e := s.db.Exec("INSERT INTO clients VALUES(?,?,'[1]',?,?,?, ?,1)", id, "alice", 1-i, 100+i*10, 200+i*20, at.Add(time.Duration(i)*time.Second).UnixMilli())
		must(t, e)
		_, e = s.db.Exec("INSERT INTO aliases VALUES(?,?,?,?)", id, "alice", at.Add(time.Duration(i-2)*time.Minute).UnixMilli(), at.Add(time.Minute).UnixMilli())
		must(t, e)
		_, e = s.db.Exec("INSERT INTO traffic VALUES(?,?,'[1]',?,?)", at.UnixMilli(), id, 10+i, 20+i)
		must(t, e)
		_, e = s.db.Exec("INSERT INTO visit_counts VALUES(?,?,1,1)", at.UnixMilli(), id)
		must(t, e)
		_, e = s.db.Exec("INSERT INTO visits(time,client_id,email,target,port,protocol,inbound,inbound_id,outbound,status) VALUES(?,?,'alice','www.baidu.com',443,'tcp','test',1,'direct','accepted')", at.Add(time.Duration(i)*time.Second).UnixMilli(), id)
		must(t, e)
	}
	_, e := s.db.Exec("INSERT INTO history_deletions VALUES(?,?,?,?)", at.Add(-time.Minute).UnixMilli(), at.UnixMilli(), old, at.UnixMilli())
	must(t, e)
	_, e = s.db.Exec("INSERT INTO visits(time,client_id,email,target,port,protocol,inbound,inbound_id,outbound,status) VALUES(?,?,'alice','hidden.test',443,'tcp','test',1,'direct','accepted')", at.Add(-time.Second).UnixMilli(), current)
	must(t, e)
	_, e = s.db.Exec("INSERT INTO visit_counts VALUES(?,?,1,1)", at.Add(-time.Minute).UnixMilli(), current)
	must(t, e)
	_, e = s.db.Exec("INSERT INTO quality VALUES(?,?,?,'history_deleted')", at.Add(-time.Minute).UnixMilli(), at.UnixMilli(), old)
	must(t, e)
	_, e = s.db.Exec("DELETE FROM meta WHERE key='identity_creation_seconds_v1'")
	must(t, e)
	_, e = s.db.Exec("CREATE TRIGGER fail_identity BEFORE UPDATE ON visits BEGIN SELECT RAISE(ABORT,'fixture rollback'); END")
	must(t, e)
	if e = s.migratePanelIdentities(); e == nil {
		t.Fatal("migration did not abort on injected failure")
	}
	var total, count int
	must(t, s.db.QueryRow("SELECT SUM(up+down),COUNT(*) FROM traffic").Scan(&total, &count))
	if total != 62 || count != 2 {
		t.Fatal("partial migration persisted", total, count)
	}
	_, e = s.db.Exec("DROP TRIGGER fail_identity")
	must(t, e)
	must(t, s.Close())
	s, e = OpenStore(cfg, at.Add(5*time.Second))
	must(t, e)
	t.Cleanup(func() { s.Close() })
	clients, e := s.Clients(at.Add(5 * time.Second))
	must(t, e)
	if len(clients) != 1 || clients[0].ID != current || clients[0].Deleted || clients[0].Up != 110 {
		t.Fatal("latest official baseline lost", clients)
	}
	q := allQuery(at.Add(-time.Minute), at.Add(time.Minute))
	q.ClientID = current
	x, e := s.Summary(q, at.Add(5*time.Second))
	must(t, e)
	if x.Total != 62 || x.VisitCount != 2 {
		t.Fatal("historical bytes/counts changed", x)
	}
	must(t, s.Sample(at.Add(10*time.Second), []Snapshot{snap(current, "alice", 115, 226)}))
	// A delayed old log in the deleted window must remain suppressed after re-keying.
	must(t, s.Ingest("migrated-replay", Checkpoint{Offset: 10}, []Visit{{At: at.Add(-time.Second), Email: "alice", Target: "hidden.test", Protocol: "tcp", Port: 443}}, at.Add(11*time.Second), false))
	must(t, s.db.QueryRow("SELECT COUNT(*) FROM visits WHERE target='hidden.test'").Scan(&count))
	if count != 0 {
		t.Fatal("deleted target restored under a merged or unmatched identity")
	}
	var mapped string
	must(t, s.db.QueryRow("SELECT client_id FROM history_deletions").Scan(&mapped))
	if mapped != current {
		t.Fatal("deletion guard not migrated")
	}
	// Re-running startup migration must not double totals or overwrite the latest sample.
	must(t, s.migratePanelIdentities())
	x, e = s.Summary(q, at.Add(11*time.Second))
	must(t, e)
	if x.Total != 73 || x.VisitCount != 2 || x.PanelUp != 115 {
		t.Fatal("migration replay or next delta incorrect", x)
	}
}
