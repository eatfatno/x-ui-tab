package monitor

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func deletionFixture(t *testing.T) (*Store, Config, time.Time, Query) {
	t.Helper()
	at := time.Date(2026, 10, 9, 1, 0, 0, 0, time.UTC)
	s, c := testStore(t, at)
	must(t, s.Sample(at, []Snapshot{snap("a", "alice", 1000, 2000), snap("b", "bob", 2000, 3000)}))
	must(t, s.Sample(at.Add(10*time.Second), []Snapshot{snap("a", "alice", 1100, 2200), snap("b", "bob", 2300, 3400)}))
	must(t, s.Ingest("file", Checkpoint{Offset: 100}, []Visit{{At: at.Add(5 * time.Second), Email: "alice", Target: "www.baidu.com", Port: 443, Protocol: "tcp"}, {At: at.Add(6 * time.Second), Email: "bob", Target: "github.com", Port: 443, Protocol: "tcp"}}, at.Add(10*time.Second), false))
	q := allQuery(at, at.Add(time.Minute))
	q.ClientID = "a"
	return s, c, at, q
}

func TestDeleteHistoryIsolatedAtomicAndPanelBaseline(t *testing.T) {
	s, _, at, q := deletionFixture(t)
	p, e := s.PreviewDeletion(q)
	must(t, e)
	if p.VisitCount != 1 || p.SummaryVisitCount != 1 || p.Total != 300 || p.ClientCount != 1 {
		t.Fatal(p)
	}
	_, e = s.DeleteHistory(q, p, at.Add(20*time.Second))
	must(t, e)
	x, e := s.Summary(q, at.Add(20*time.Second))
	must(t, e)
	if x.Total != 0 || x.VisitCount != 0 || x.PanelUp != 1100 || x.PanelDown != 2200 || !strings.Contains(strings.Join(x.Quality, ","), "history_deleted") {
		t.Fatal(x)
	}
	page, e := s.Targets(q, at.Add(20*time.Second))
	must(t, e)
	if page.Total != 0 {
		t.Fatal(page)
	}
	q.ClientID = "b"
	x, e = s.Summary(q, at.Add(20*time.Second))
	must(t, e)
	if x.Total != 700 || x.VisitCount != 1 {
		t.Fatal("other client changed", x)
	}
	// Force a failure after detail deletion: the entire transaction must roll back.
	p, e = s.PreviewDeletion(q)
	must(t, e)
	_, e = s.db.Exec("CREATE TRIGGER fail_delete BEFORE DELETE ON traffic BEGIN SELECT RAISE(ABORT,'test rollback'); END;")
	must(t, e)
	if _, e = s.DeleteHistory(q, p, at.Add(30*time.Second)); e == nil {
		t.Fatal("expected rollback")
	}
	page2, e := s.Visits(q, at.Add(30*time.Second))
	must(t, e)
	if page2.Total != 1 {
		t.Fatal("partial deletion committed")
	}
}

func TestDeletePreviewRejectsChangesAndNarrowFilters(t *testing.T) {
	s, _, at, q := deletionFixture(t)
	p, e := s.PreviewDeletion(q)
	must(t, e)
	_, e = s.db.Exec("UPDATE visits SET target='youtube.com' WHERE client_id='a'")
	must(t, e)
	if _, e = s.DeleteHistory(q, p, at); !errors.Is(e, ErrDeletionChanged) {
		t.Fatal("same count modified row accepted", e)
	}
	q.Target = "youtube"
	if _, e = s.PreviewDeletion(q); e == nil {
		t.Fatal("target delete accepted")
	}
	q.Target = ""
	q.InboundID = 1
	if _, e = s.PreviewDeletion(q); e == nil {
		t.Fatal("inbound delete accepted")
	}
	q.InboundID = 0
	q.Start = q.Start.Add(time.Second)
	if _, e = s.PreviewDeletion(q); e == nil {
		t.Fatal("partial minute accepted")
	}
}

func TestDeletionSurvivesRestartReplayAndSampling(t *testing.T) {
	s, c, at, q := deletionFixture(t)
	p, e := s.PreviewDeletion(q)
	must(t, e)
	_, e = s.DeleteHistory(q, p, at.Add(20*time.Second))
	must(t, e)
	must(t, s.Close())
	s, e = OpenStore(c, at.Add(30*time.Second))
	must(t, e)
	defer s.Close()
	must(t, s.Ingest("copied-log", Checkpoint{Offset: 200}, []Visit{{At: at.Add(5 * time.Second), Email: "alice", Target: "baidu.com", Port: 443}}, at.Add(30*time.Second), false))
	must(t, s.Sample(at.Add(40*time.Second), []Snapshot{snap("a", "alice", 1200, 2400), snap("b", "bob", 2300, 3400)}))
	// A sample straddling the deletion boundary is intentionally excluded. Its
	// next sample starts from a clean baseline; pre-deletion bytes cannot return.
	must(t, s.Sample(at.Add(70*time.Second), []Snapshot{snap("a", "alice", 1300, 2600), snap("b", "bob", 2300, 3400)}))
	must(t, s.Sample(at.Add(80*time.Second), []Snapshot{snap("a", "alice", 1310, 2620), snap("b", "bob", 2300, 3400)}))
	q.End = at.Add(2 * time.Minute)
	x, e := s.Summary(q, at.Add(90*time.Second))
	must(t, e)
	if x.VisitCount != 0 || x.Up != 10 || x.Down != 20 || x.PanelUp != 1310 {
		t.Fatal("deleted usage returned", x)
	}
	cp, e := s.Checkpoint("copied-log")
	must(t, e)
	if cp.Offset != 200 {
		t.Fatal("suppressed log checkpoint not advanced")
	}
}

func TestDeleteExpiredDetailsStillRemoves35DayAggregates(t *testing.T) {
	s, _, at, q := deletionFixture(t)
	now := at.AddDate(0, 0, 8)
	must(t, s.Prune(now))
	p, e := s.PreviewDeletion(q)
	must(t, e)
	if p.VisitCount != 0 || p.SummaryVisitCount != 1 || p.Total != 300 {
		t.Fatal(p)
	}
	_, e = s.DeleteHistory(q, p, now)
	must(t, e)
	x, e := s.Summary(q, now)
	must(t, e)
	if x.Total != 0 || x.VisitCount != 0 {
		t.Fatal(x)
	}
	q.ClientID = ""
	p, e = s.PreviewDeletion(q)
	must(t, e)
	_, e = s.DeleteHistory(q, p, now)
	must(t, e)
	x, e = s.Summary(q, now)
	must(t, e)
	if x.Total != 0 || x.VisitCount != 0 {
		t.Fatal(x)
	}
}
