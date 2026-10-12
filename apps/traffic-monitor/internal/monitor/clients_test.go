package monitor

import (
	"testing"
	"time"
)

func TestDeletedClientOptionsFollowRemainingHistoryWithoutLosingIdentity(t *testing.T) {
	s, _, at, q := deletionFixture(t)
	// Retire old identities and create an empty current client with the same email.
	must(t, s.Sample(at.Add(30*time.Second), []Snapshot{snap("new-a", "alice", 0, 0), snap("empty", "unused", 0, 0)}))
	must(t, s.Sample(at.Add(40*time.Second), []Snapshot{snap("new-a", "alice", 0, 0)}))
	listed := func(now time.Time) map[string]bool {
		t.Helper()
		clients, err := s.Clients(now)
		must(t, err)
		ids := map[string]bool{}
		for _, client := range clients {
			ids[client.ID] = true
		}
		return ids
	}
	ids := listed(at.Add(time.Minute))
	if len(ids) != 3 || !ids["a"] || !ids["b"] || !ids["new-a"] || ids["empty"] {
		t.Fatal("current and retained historical clients should be selectable", ids)
	}
	// Pruned details must not hide an identity with valid 35-day aggregates.
	now := at.AddDate(0, 0, 8)
	must(t, s.Prune(now))
	if ids = listed(now); !ids["a"] || !ids["b"] {
		t.Fatal("summary-only identities disappeared", ids)
	}
	preview, err := s.PreviewDeletion(q)
	must(t, err)
	_, err = s.DeleteHistory(q, preview, now)
	must(t, err)
	if ids = listed(now); ids["a"] || !ids["b"] || !ids["new-a"] {
		t.Fatal("cleared identity remained selectable or another identity disappeared", ids)
	}
	var count int
	must(t, s.db.QueryRow("SELECT COUNT(*) FROM clients WHERE id='a'").Scan(&count))
	if count != 1 {
		t.Fatal("identity needed by replay protection was physically removed")
	}
	must(t, s.Ingest("old-copy", Checkpoint{Offset: 200}, []Visit{{At: at.Add(5 * time.Second), Email: "alice", Target: "www.baidu.com"}}, now, false))
	if listed(now)["a"] {
		t.Fatal("deleted history was reimported")
	}
	// Even before the next prune, expired-only history cannot keep options alive.
	ids = listed(at.AddDate(0, 0, 36))
	if len(ids) != 1 || !ids["new-a"] {
		t.Fatal("expired historical options remained", ids)
	}
}
