package monitor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type resetFixture struct {
	mu       sync.Mutex
	store    *Store
	cfg      Config
	server   *Server
	at       time.Time
	changed  bool
	reject   bool
	readFail bool
	writes   int
	up, down int64
}

func newResetFixture(t *testing.T) *resetFixture {
	t.Helper()
	at := time.Date(2026, 10, 10, 5, 0, 0, 0, time.UTC)
	s, c := testStore(t, at.Add(-time.Minute))
	c.WebBasePath = "/secret/"
	a, b := "local:1:1000", "local:2:2000"
	must(t, s.Sample(at.Add(-time.Minute), []Snapshot{snap(a, "alice", 100, 200), snap(b, "bob", 100, 200)}))
	must(t, s.Sample(at.Add(-50*time.Second), []Snapshot{snap(a, "alice", 130, 240), snap(b, "bob", 170, 280)}))
	must(t, s.Ingest("reset-visit", Checkpoint{Offset: 10}, []Visit{{At: at.Add(-40 * time.Second), Email: "alice", Target: "www.baidu.com", Port: 443, Protocol: "tcp"}}, at.Add(-30*time.Second), false))
	f := &resetFixture{store: s, cfg: c, at: at, up: 130, down: 240}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("Authorization") != "" {
			t.Error("collector credential used for reset")
		}
		cookie, e := r.Cookie("session")
		if e != nil || !strings.HasPrefix(cookie.Value, "valid") {
			http.Error(w, "unauthorized", 401)
			return
		}
		switch r.URL.Path {
		case "/secret/panel/api/server/status":
			success(w, map[string]any{"panelVersion": "3.9.0"})
		case "/secret/csrf-token":
			http.SetCookie(w, &http.Cookie{Name: "csrf_nonce", Value: "bound", Path: "/secret/"})
			success(w, "official-csrf")
		case "/secret/panel/api/clients/list":
			if f.readFail && f.writes > 0 {
				http.Error(w, "unavailable", 503)
				return
			}
			email := "alice"
			if f.changed {
				email = "renamed"
			}
			success(w, []any{map[string]any{"id": 1, "createdAt": 1000, "email": email, "enable": true, "inboundIds": []int{1}, "traffic": map[string]any{"up": f.up, "down": f.down}}, map[string]any{"id": 2, "createdAt": 2000, "email": "bob", "enable": true, "inboundIds": []int{2}, "traffic": map[string]any{"up": 170, "down": 280}}})
		case "/secret/panel/api/clients/bulkResetTraffic":
			if r.Method != "POST" || r.Header.Get("X-CSRF-Token") != "official-csrf" {
				t.Error("official write missing method/CSRF")
			}
			if cookie, e := r.Cookie("csrf_nonce"); e != nil || cookie.Value != "bound" {
				t.Error("official CSRF cookie update lost")
			}
			var body struct {
				Emails []string `json:"emails"`
			}
			must(t, json.NewDecoder(r.Body).Decode(&body))
			if len(body.Emails) != 1 || body.Emails[0] != "alice" {
				t.Error("incorrect reset scope", body)
			}
			f.writes++
			if f.reject {
				failure(w, 403, "denied")
				return
			}
			f.up = 0
			f.down = 0
			success(w, map[string]int{"affected": 1})
		default:
			t.Error("unexpected upstream route", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	c.Upstream = upstream.URL
	f.cfg = c
	f.server = NewServer(c, s, NewCollector(c, s))
	clock := at
	f.server.now = func() time.Time { clock = clock.Add(time.Millisecond); return clock }
	return f
}

func (f *resetFixture) request(method, path, cookie, origin, csrf, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://example.com/secret/traffic-monitor/api/v1/"+path, strings.NewReader(body))
	r.Header.Set("Cookie", cookie)
	r.Header.Set("Origin", origin)
	r.Header.Set("X-Monitor-CSRF", csrf)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, r)
	return w
}
func (f *resetFixture) preview(t *testing.T) string {
	t.Helper()
	w := f.request("GET", "panel/reset-preview?client_id=local:1:1000", "session=valid-a", "", "", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result struct {
		Obj panelResetPreview `json:"obj"`
	}
	must(t, json.Unmarshal(w.Body.Bytes(), &result))
	if result.Obj.Up != 130 || result.Obj.Down != 240 || len(result.Obj.Clients) != 1 {
		t.Fatal(result)
	}
	return result.Obj.Token
}
func (f *resetFixture) reset(token string) *httptest.ResponseRecorder {
	return f.request("POST", "panel/reset", "session=valid-a", "http://example.com", token, fmt.Sprintf(`{"preview_token":%q}`, token))
}

func TestPanelResetSessionScopeCSRFOneUseAndHistory(t *testing.T) {
	f := newResetFixture(t)
	if w := f.request("GET", "panel/reset-preview", "", "", "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := f.request("GET", "panel/reset", "session=valid-a", "", "", ""); w.Code != 405 {
		t.Fatal(w.Code)
	}
	if w := f.request("GET", "panel/reset-preview?start=2026-10-10", "session=valid-a", "", "", ""); w.Code != 400 {
		t.Fatal(w.Code)
	}
	token := f.preview(t)
	body := fmt.Sprintf(`{"preview_token":%q}`, token)
	for _, tc := range []struct{ cookie, origin, csrf string }{
		{"session=valid-a", "https://evil.test", token}, {"session=valid-a", "http://example.com", "wrong"}, {"session=valid-b", "http://example.com", token},
	} {
		if w := f.request("POST", "panel/reset", tc.cookie, tc.origin, tc.csrf, body); w.Code != 403 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if f.writes != 0 {
		t.Fatal("unauthorized write")
	}
	if w := f.reset(token); w.Code != 200 || !strings.Contains(w.Body.String(), `"synced":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := f.reset(token); w.Code != 409 {
		t.Fatal("preview reused", w.Code)
	}
	q := allQuery(f.at.Add(-time.Minute), f.at.Add(time.Minute))
	q.ClientID = "local:1:1000"
	summary, e := f.store.Summary(q, f.at)
	must(t, e)
	if summary.PanelUp != 0 || summary.PanelDown != 0 || summary.Total != 70 || summary.VisitCount != 1 {
		t.Fatal("reset changed history or failed", summary)
	}
	if summary.LastVisitAt == "" {
		t.Fatal("latest-visit hint missing")
	}
	must(t, f.store.Sample(f.at.Add(10*time.Second), []Snapshot{snap("local:1:1000", "alice", 5, 8), snap("local:2:2000", "bob", 170, 280)}))
	summary, e = f.store.Summary(q, f.at.Add(10*time.Second))
	must(t, e)
	if summary.Total != 83 {
		t.Fatal("post-reset delta incorrect", summary)
	}
	q.ClientID = "local:2:2000"
	other, e := f.store.Summary(q, f.at)
	must(t, e)
	if other.PanelUp != 170 || other.Total != 150 {
		t.Fatal("another client reset", other)
	}
}

func TestPanelResetRejectsRenameExpiryAndKeepsDurableBaselineOnUncertainWrite(t *testing.T) {
	t.Run("changed identity", func(t *testing.T) {
		f := newResetFixture(t)
		token := f.preview(t)
		f.mu.Lock()
		f.changed = true
		f.mu.Unlock()
		if w := f.reset(token); w.Code != 409 {
			t.Fatal(w.Code, w.Body.String())
		}
		if f.writes != 0 {
			t.Fatal("changed identity written")
		}
	})
	t.Run("expired", func(t *testing.T) {
		f := newResetFixture(t)
		token := f.preview(t)
		f.server.now = func() time.Time { return f.at.Add(6 * time.Minute) }
		if w := f.reset(token); w.Code != 409 {
			t.Fatal(w.Code)
		}
	})
	for _, rejected := range []bool{true, false} {
		t.Run(fmt.Sprintf("upstream_rejected_%v", rejected), func(t *testing.T) {
			f := newResetFixture(t)
			token := f.preview(t)
			f.mu.Lock()
			f.reject = rejected
			f.readFail = !rejected
			f.mu.Unlock()
			w := f.reset(token)
			if rejected && w.Code != 502 {
				t.Fatal(w.Code, w.Body.String())
			}
			if !rejected && (w.Code != 200 || !strings.Contains(w.Body.String(), `"synced":false`)) {
				t.Fatal(w.Code, w.Body.String())
			}
			must(t, f.store.Close())
			reopened, e := OpenStore(f.cfg, f.at)
			must(t, e)
			defer reopened.Close()
			var has int
			must(t, reopened.db.QueryRow("SELECT has_traffic FROM clients WHERE id='local:1:1000'").Scan(&has))
			if has != 0 {
				t.Fatal("uncertain reset lost durable guard")
			}
			// Even a post-reset counter already larger than the old value is a new
			// baseline, rather than a fabricated large interval increment.
			must(t, reopened.Sample(f.at.Add(10*time.Second), []Snapshot{snap("local:1:1000", "alice", 999, 1999), snap("local:2:2000", "bob", 170, 280)}))
			q := allQuery(f.at.Add(-time.Minute), f.at.Add(time.Minute))
			q.ClientID = "local:1:1000"
			x, e := reopened.Summary(q, f.at)
			must(t, e)
			if x.Total != 70 {
				t.Fatal(x)
			}
			must(t, reopened.Sample(f.at.Add(20*time.Second), []Snapshot{snap("local:1:1000", "alice", 1000, 2001), snap("local:2:2000", "bob", 170, 280)}))
			x, e = reopened.Summary(q, f.at)
			must(t, e)
			if x.Total != 73 {
				t.Fatal(x)
			}
		})
	}
}
