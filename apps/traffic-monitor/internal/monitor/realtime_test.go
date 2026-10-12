package monitor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRealtimeOnlineRatesResetAndStaleAreExplicit(t *testing.T) {
	at := time.Date(2026, 10, 10, 5, 0, 0, 0, time.UTC)
	s, cfg := testStore(t, at)
	c := NewCollector(cfg, s)
	c.updateRealtime(at, []Snapshot{snap("alice", "alice", 100, 200), snap("bob", "bob", 0, 0)}, []string{"alice"}, true)
	first := c.Realtime(at, "", 0)
	if len(first.Items) != 2 || first.Items[0].Status != "online" || first.Items[0].UpRate != nil || first.Items[1].Status != "offline" {
		t.Fatal(first)
	}
	c.updateRealtime(at.Add(10*time.Second), []Snapshot{snap("alice", "alice", 2100, 8200), snap("bob", "bob", 0, 0)}, []string{"alice", "bob"}, true)
	live := c.Realtime(at.Add(11*time.Second), "alice", 0)
	row := live.Items[0]
	if len(live.Items) != 1 || row.RateState != "ok" || *row.Up != 2000 || *row.Down != 8000 || *row.UpRate != 200 || *row.DownRate != 800 {
		t.Fatal(row)
	}
	// Idle live connections remain online even when the byte delta is zero.
	bob := c.Realtime(at.Add(11*time.Second), "bob", 0).Items[0]
	if bob.Status != "online" || bob.UpRate == nil || *bob.UpRate != 0 {
		t.Fatal(bob)
	}
	if len(c.Realtime(at.Add(11*time.Second), "alice", 99).Items) != 0 {
		t.Fatal("inbound filter ignored")
	}
	c.invalidateLiveBaselines([]resetClient{{ID: "alice"}})
	if r := c.Realtime(at.Add(12*time.Second), "alice", 0).Items[0]; r.UpRate != nil || r.RateState != "reset" {
		t.Fatal(r)
	}
	c.updateRealtime(at.Add(20*time.Second), []Snapshot{snap("alice", "alice", 5000, 15000), snap("bob", "bob", 0, 0)}, nil, false)
	row = c.Realtime(at.Add(21*time.Second), "alice", 0).Items[0]
	if row.UpRate != nil || row.Status != "unknown" {
		t.Fatal("reset baseline or missing online state invented", row)
	}
	c.updateRealtime(at.Add(30*time.Second), []Snapshot{snap("alice", "alice", 5100, 15200)}, []string{"alice"}, true)
	row = c.Realtime(at.Add(31*time.Second), "alice", 0).Items[0]
	if *row.UpRate != 10 || *row.DownRate != 20 {
		t.Fatal(row)
	}
	c.updateRealtime(at.Add(40*time.Second), []Snapshot{snap("alice", "alice", 0, 0)}, nil, true)
	if r := c.Realtime(at.Add(41*time.Second), "alice", 0).Items[0]; r.RateState != "reset" || r.UpRate != nil {
		t.Fatal(r)
	}
	c.updateRealtime(at.Add(80*time.Second), []Snapshot{snap("alice", "alice", 100, 100)}, nil, true)
	if r := c.Realtime(at.Add(81*time.Second), "alice", 0).Items[0]; r.RateState != "gap" || r.UpRate != nil {
		t.Fatal(r)
	}
	stale := c.Realtime(at.Add(120*time.Second), "", 0)
	if !stale.Stale || stale.OnlineAvailable || stale.Items[0].Status != "unknown" || stale.Items[0].UpRate != nil {
		t.Fatal(stale)
	}
	c.realtimeUnavailable()
	if !c.Realtime(at.Add(81*time.Second), "", 0).Stale {
		t.Fatal("failure kept stale values live")
	}
	c.updateRealtime(at.Add(90*time.Second), []Snapshot{snap("alice", "alice", 200, 200)}, nil, true)
	if r := c.Realtime(at.Add(91*time.Second), "alice", 0).Items[0]; r.UpRate != nil {
		t.Fatal("reconnect did not rebuild baseline")
	}
	var stored int
	must(t, s.db.QueryRow("SELECT COUNT(*) FROM traffic").Scan(&stored))
	if stored != 0 {
		t.Fatal("realtime state persisted extra history")
	}
}

func TestRealtimeOnlineRouteIsFixedReadAndBrowserEndpointIsSessionProtected(t *testing.T) {
	at := time.Now()
	s, cfg := testStore(t, at)
	cfg.APITokenFile = filepath.Join(t.TempDir(), "token")
	must(t, os.WriteFile(cfg.APITokenFile, []byte("fixture-token"), 0600))
	reads := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/panel/api/clients/onlines":
			if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer fixture-token" || r.ContentLength > 0 {
				t.Error("online read request changed")
			}
			reads++
			success(w, []string{"alice"})
		case "/panel/api/server/status":
			if r.Header.Get("Authorization") != "" {
				t.Error("browser borrowed collector token")
			}
			cookie, e := r.Cookie("session")
			if e != nil || cookie.Value != "valid" {
				failure(w, 401, "denied")
				return
			}
			success(w, map[string]any{"panelVersion": "3.9.0"})
		default:
			t.Error("unexpected route", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	cfg.Upstream = upstream.URL
	c := NewCollector(cfg, s)
	online, e := c.fetchOnline(context.Background())
	must(t, e)
	c.updateRealtime(at, []Snapshot{snap("alice", "alice", 0, 0)}, online, true)
	server := NewServer(cfg, s, c)
	for _, tc := range []struct {
		path, cookie string
		status       int
	}{
		{"realtime", "", 401}, {"realtime", "session=valid", 200}, {"realtime?start=2026-10-10", "session=valid", 400}, {"realtime?inbound_id=-1", "session=valid", 400}, {"realtime?client_id=alice", "session=valid", 200},
	} {
		r := httptest.NewRequest("GET", "http://example.com/traffic-monitor/api/v1/"+tc.path, nil)
		r.Header.Set("Cookie", tc.cookie)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
	if reads != 1 {
		t.Fatal("browser polling called online API again", reads)
	}
}
