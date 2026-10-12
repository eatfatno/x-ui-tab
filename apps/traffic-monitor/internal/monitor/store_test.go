package monitor

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T, start time.Time) (*Store, Config) {
	t.Helper()
	dir := t.TempDir()
	c := DefaultConfig()
	c.DBPath = filepath.Join(dir, "monitor.db")
	c.AccessLogPath = filepath.Join(dir, "access.log")
	c.APITokenFile = filepath.Join(dir, "token")
	c.WebDir = filepath.Join(dir, "web")
	s, e := OpenStore(c, start)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s, c
}
func must(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}
func snap(id, email string, up, down int64) Snapshot {
	return Snapshot{Client: Client{ID: id, Email: email, InboundIDs: []int{1}, Up: up, Down: down}, HasTraffic: true}
}
func allQuery(start, end time.Time) Query {
	return Query{Start: start, End: end, Page: 1, PageSize: 50}
}

func TestIdleSamplingKeepsBaselineWithoutEmptyTrafficBuckets(t *testing.T) {
	start := time.Date(2026, 10, 9, 1, 0, 0, 0, time.UTC)
	s, _ := testStore(t, start)
	for i := 0; i <= 12; i++ {
		must(t, s.Sample(start.Add(time.Duration(i)*10*time.Second), []Snapshot{snap("a", "alice", 100, 200)}))
	}
	var count int
	must(t, s.db.QueryRow("SELECT COUNT(*) FROM traffic").Scan(&count))
	if count != 0 {
		t.Fatal("idle traffic stored", count)
	}
	must(t, s.Sample(start.Add(130*time.Second), []Snapshot{snap("a", "alice", 110, 220)}))
	x, e := s.Summary(allQuery(start, start.Add(3*time.Minute)), start.Add(130*time.Second))
	must(t, e)
	if x.Total != 30 || strings.Contains(strings.Join(x.Quality, ","), "collection_gap") {
		t.Fatal("idle baseline lost", x)
	}
}

func TestFirstMinuteBaselineResetAndRetention(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 20, 0, time.UTC)
	s, _ := testStore(t, now)
	must(t, s.Sample(now, []Snapshot{snap("local:1", "alice", 1000, 2000)}))
	must(t, s.Sample(now.Add(10*time.Second), []Snapshot{snap("local:1", "alice", 1200, 2400)}))
	v := Visit{At: now.Add(5 * time.Second), Email: "alice", Target: "example.com", Port: 443, Protocol: "tcp", Inbound: "inbound-1", InboundID: 1, Status: "accepted"}
	must(t, s.Ingest("a", Checkpoint{Offset: 4}, []Visit{v}, now.Add(10*time.Second), false))
	q := allQuery(now.Truncate(time.Hour), now.Add(time.Hour))
	summary, e := s.Summary(q, now.Add(20*time.Second))
	must(t, e)
	if summary.Up != 200 || summary.Down != 400 || summary.VisitCount != 1 || summary.PanelUp != 1200 {
		t.Fatalf("first minute lost or baseline counted: %+v", summary)
	}
	must(t, s.Sample(now.Add(20*time.Second), []Snapshot{snap("local:1", "alice", 0, 0)}))
	summary, e = s.Summary(q, now.Add(20*time.Second))
	must(t, e)
	if summary.Up != 200 || !strings.Contains(strings.Join(summary.Quality, ","), "counter_reset") {
		t.Fatalf("reset: %+v", summary)
	}
	must(t, s.Prune(now.Add(8*24*time.Hour)))
	summary, e = s.Summary(q, now.Add(8*24*time.Hour))
	must(t, e)
	page, e := s.Visits(q, now.Add(8*24*time.Hour))
	must(t, e)
	if len(page.Items) != 0 || summary.VisitCount != 1 || summary.Up != 200 {
		t.Fatalf("7 day cleanup removed aggregates: %+v %+v", summary, page)
	}
	must(t, s.Prune(now.Add(36*24*time.Hour)))
	summary, e = s.Summary(q, now.Add(36*24*time.Hour))
	must(t, e)
	if summary.Up != 0 || summary.VisitCount != 0 {
		t.Fatal("expired summary remains")
	}
}

func TestRenameReuseAndUnmatchedIdentity(t *testing.T) {
	now := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	s, _ := testStore(t, now)
	must(t, s.Sample(now, []Snapshot{snap("local:1:100", "old", 0, 0)}))
	must(t, s.Sample(now.Add(time.Minute), []Snapshot{snap("local:1:100", "renamed", 1, 1)}))
	must(t, s.Sample(now.Add(2*time.Minute), []Snapshot{snap("local:2:200", "old", 0, 0)}))
	vs := []Visit{{At: now.Add(30 * time.Second), Email: "old", Target: "a"}, {At: now.Add(90 * time.Second), Email: "renamed", Target: "b"}, {At: now.Add(150 * time.Second), Email: "old", Target: "c"}, {At: now.Add(160 * time.Second), Email: "unknown", Target: "d"}}
	must(t, s.Ingest("x", Checkpoint{Offset: 10}, vs, now.Add(3*time.Minute), false))
	p, e := s.Visits(allQuery(now, now.Add(4*time.Minute)), now.Add(3*time.Minute))
	must(t, e)
	got := map[string]string{}
	for _, v := range p.Items {
		got[v.Target] = v.ClientID
	}
	if got["a"] != "local:1:100" || got["b"] != "local:1:100" || got["c"] != "local:2:200" || got["d"] != "" {
		t.Fatal(got)
	}
}

func TestCrossDayGapNotAllocatedAndParallelReaders(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Date(2026, 10, 8, 23, 59, 0, 0, loc)
	s, _ := testStore(t, now)
	must(t, s.Sample(now, []Snapshot{snap("a", "alice", 100, 100)}))
	must(t, s.Sample(now.Add(2*time.Minute), []Snapshot{snap("a", "alice", 900, 900)}))
	summary, e := s.Summary(allQuery(now.Add(-time.Minute), now.Add(4*time.Minute)), now.Add(2*time.Minute))
	must(t, e)
	if summary.Up != 0 || !strings.Contains(strings.Join(summary.Quality, ","), "collection_gap") {
		t.Fatal(summary)
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				if _, e := s.Clients(now); e != nil {
					t.Error(e)
				}
			}
		}()
	}
	for i := 1; i < 10; i++ {
		must(t, s.Sample(now.Add(2*time.Minute+time.Duration(i)*time.Second), []Snapshot{snap("a", "alice", 900+int64(i), 900)}))
	}
	wg.Wait()
}

func TestRollbackAndPersistence(t *testing.T) {
	now := time.Now()
	s, c := testStore(t, now)
	must(t, s.Sample(now, []Snapshot{snap("a", "a", 0, 0)}))
	e := s.Sample(now.Add(time.Second), []Snapshot{snap("a", "a", 10, 10), snap("b", "", 0, 0)})
	if e == nil {
		t.Fatal("invalid batch accepted")
	}
	clients, e := s.Clients(now)
	must(t, e)
	if len(clients) != 1 || clients[0].Up != 0 {
		t.Fatal("partial transaction")
	}
	must(t, s.Close())
	s2, e := OpenStore(c, now.Add(time.Hour))
	must(t, e)
	defer s2.Close()
	if !s2.started.Equal(s.started) {
		t.Fatal("start reset on reopen")
	}
	clients, e = s2.Clients(now.Add(time.Hour))
	must(t, e)
	if fmt.Sprint(clients[0].Up) != "0" {
		t.Fatal(clients)
	}
}

func TestInternalAPILogExcludedAndLegacyRowsRemoved(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	s, _ := testStore(t, now.Add(-time.Minute))
	must(t, s.Sample(now.Add(-time.Minute), []Snapshot{snap("a", "alice", 0, 0)}))
	loc, _ := time.LoadLocation("Asia/Shanghai")
	v, e := ParseAccess(now.In(loc).Format("2006/01/02 15:04:05")+" from 127.0.0.1:50022 accepted tcp:127.0.0.1:62789 [api -> api]", loc)
	must(t, e)
	actual := Visit{At: now, Email: "alice", Target: "site.example", Inbound: "custom-tag", InboundID: 1}
	must(t, s.Ingest("log", Checkpoint{Offset: 200}, []Visit{v, actual}, now, false))
	q := allQuery(now.Add(-time.Minute), now.Add(time.Minute))
	p, e := s.Visits(q, now)
	must(t, e)
	if p.Total != 1 || p.Items[0].Target != "site.example" {
		t.Fatal("internal traffic counted", p)
	}
	// Reproduce exactly the earlier development-row shape to verify repair.
	_, e = s.db.Exec("INSERT INTO visits(time,client_id,email,target,port,protocol,inbound,inbound_id,outbound,status) VALUES(?,'','','127.0.0.1',62789,'tcp','api',0,'api','accepted')", now.UnixMilli())
	must(t, e)
	_, e = s.db.Exec("INSERT INTO visit_counts(time,client_id,inbound_id,count) VALUES(?,'',0,1)", now.Truncate(time.Minute).UnixMilli())
	must(t, e)
	must(t, s.RecordIssue(now, "unmatched_identity"))
	must(t, s.removeInternalVisits())
	p, e = s.Visits(q, now)
	must(t, e)
	summary, e := s.Summary(q, now)
	must(t, e)
	if p.Total != 1 || summary.VisitCount != 1 || strings.Contains(strings.Join(summary.Quality, ","), "unmatched_identity") {
		t.Fatalf("repair changed client row or left counts: %+v %+v", p, summary)
	}
}

func TestCustomInboundMappingHasTimeBounds(t *testing.T) {
	now := time.Now().Truncate(time.Minute)
	s, _ := testStore(t, now)
	must(t, s.Sample(now, []Snapshot{snap("a", "alice", 0, 0)}))
	must(t, s.SyncInbounds(now, map[string]int{"custom-tag": 5}))
	must(t, s.SyncInbounds(now.Add(time.Minute), map[string]int{"renamed-tag": 5}))
	must(t, s.Ingest("log", Checkpoint{Offset: 4}, []Visit{{At: now.Add(30 * time.Second), Email: "alice", Inbound: "custom-tag", Target: "old.test"}, {At: now.Add(90 * time.Second), Email: "alice", Inbound: "renamed-tag", Target: "new.test"}}, now.Add(2*time.Minute), false))
	q := allQuery(now, now.Add(3*time.Minute))
	q.InboundID = 5
	p, e := s.Visits(q, now.Add(2*time.Minute))
	must(t, e)
	if p.Total != 2 {
		t.Fatal(p)
	}
	var closed int
	must(t, s.db.QueryRow("SELECT COUNT(*) FROM inbound_aliases WHERE tag='custom-tag' AND valid_to IS NOT NULL").Scan(&closed))
	if closed != 1 {
		t.Fatal("stale inbound alias retained open")
	}
}
