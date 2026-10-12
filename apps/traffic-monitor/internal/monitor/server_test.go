package monitor

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBrowserSessionGateTokenIsolationAndCSP(t *testing.T) {
	now := time.Now()
	s, c := testStore(t, now)
	c.WebBasePath = "/secret/"
	c.PanelVersion = "3.9.0"
	must(t, os.MkdirAll(c.WebDir, 0700))
	must(t, os.WriteFile(filepath.Join(c.WebDir, "index.html"), []byte("monitor-page"), 0600))
	must(t, os.WriteFile(filepath.Join(c.WebDir, "adapter.js"), []byte("adapter"), 0600))
	authProbes := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/secret/panel/api/server/status" {
			authProbes++
			if r.Header.Get("Authorization") != "" || r.Header.Get("X-Forwarded-User") != "" {
				t.Error("credential contamination")
			}
			if r.Header.Get("Cookie") != "session=valid" {
				http.Error(w, "no", 401)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "obj": map[string]any{"cpu": 1}})
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'nonce-abc'; frame-ancestors 'none'")
		w.Header().Set("X-Frame-Options", "DENY")
		_, _ = io.WriteString(w, "<html><body>official</body></html>")
	}))
	defer up.Close()
	c.Upstream = up.URL
	server := NewServer(c, s, NewCollector(c, s))
	for _, tc := range []struct {
		path, cookie, auth string
		code               int
	}{{"/secret/traffic-monitor/api/v1/clients", "", "Bearer collector", 401}, {"/secret/traffic-monitor/", "", "", 303}, {"/secret/traffic-monitor/api/v1/clients", "session=invalid", "Bearer collector", 401}, {"/secret/traffic-monitor/api/v1/clients", "session=valid", "Bearer attacker", 200}, {"/secret/traffic-monitor/", "session=valid", "", 200}, {"/secret/traffic-monitor/adapter.js", "", "", 200}} {
		r := httptest.NewRequest("GET", tc.path, nil)
		r.Header.Set("Cookie", tc.cookie)
		r.Header.Set("Authorization", tc.auth)
		r.Header.Set("X-Forwarded-User", "admin")
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatalf("%s want%d got%d body%s", tc.path, tc.code, w.Code, w.Body.String())
		}
	}
	if authProbes != 3 {
		t.Fatal("unexpected probes", authProbes)
	}
	w := httptest.NewRecorder()
	server.ServeHTTP(w, httptest.NewRequest("GET", "/secret/panel/", nil))
	body := w.Body.String()
	if !strings.Contains(body, `src="/secret/traffic-monitor/adapter.js"`) || w.Header().Get("X-Frame-Options") != "DENY" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "nonce-abc") {
		t.Fatalf("injection weakened upstream headers: %s %+v", body, w.Header())
	}
	c.PanelVersion = "4.0.0"
	server = NewServer(c, s, NewCollector(c, s))
	w = httptest.NewRecorder()
	server.ServeHTTP(w, httptest.NewRequest("GET", "/secret/panel/", nil))
	if strings.Contains(w.Body.String(), "adapter.js") {
		t.Fatal("unknown adapter injected")
	}
}

func TestSessionRejectsRedirectAndFakeSuccess(t *testing.T) {
	for _, mode := range []string{"redirect", "html", "null", "failure"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now()
			s, c := testStore(t, now)
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "redirect":
					http.Redirect(w, r, "/login", 302)
				case "html":
					w.Header().Set("Content-Type", "text/html")
					io.WriteString(w, `{"success":true,"obj":{}}`)
				case "null":
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, `{"success":true,"obj":null}`)
				case "failure":
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, `{"success":false,"obj":{}}`)
				}
			}))
			defer up.Close()
			c.Upstream = up.URL
			server := NewServer(c, s, NewCollector(c, s))
			r := httptest.NewRequest("GET", "/traffic-monitor/api/v1/health", nil)
			r.Header.Set("Cookie", "x=y")
			w := httptest.NewRecorder()
			server.ServeHTTP(w, r)
			if w.Code != 401 {
				t.Fatal(w.Code)
			}
		})
	}
}

func TestDateQueryValidation(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Date(2026, 10, 8, 8, 0, 0, 0, time.UTC)
	q, e := parseQuery(httptest.NewRequest("GET", "/", nil), loc, now)
	must(t, e)
	if q.Start.Hour() != 0 || q.Start.Location() != loc || q.End.Sub(q.Start) != 24*time.Hour {
		t.Fatal(q)
	}
	for _, url := range []string{"/?start=2026-10-01", "/?start=2026-01-01T00:00:00Z&end=2026-10-01T00:00:00Z", "/?page_size=201", "/?inbound_id=-1", "/?page=0"} {
		if _, e = parseQuery(httptest.NewRequest("GET", url, nil), loc, now); e == nil {
			t.Fatal("invalid accepted", url)
		}
	}
}

func TestSessionOutageIs503AndUnknownUpgradeIsDetected(t *testing.T) {
	now := time.Now()
	s, c := testStore(t, now)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "server/status") {
			http.Error(w, "offline", 503)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, `<html><head><script>window.X_UI_CUR_VER="v4.0.0";</script></head><body>new panel</body></html>`)
	}))
	defer up.Close()
	c.Upstream = up.URL
	col := NewCollector(c, s)
	server := NewServer(c, s, col)
	r := httptest.NewRequest("GET", "/traffic-monitor/api/v1/summary", nil)
	r.Header.Set("Cookie", "session=valid")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, r)
	if w.Code != 503 || w.Header().Get("Location") != "" {
		t.Fatal("outage logged user out", w.Code, w.Header())
	}
	w = httptest.NewRecorder()
	server.ServeHTTP(w, httptest.NewRequest("GET", "/panel/", nil))
	if strings.Contains(w.Body.String(), "adapter.js") || col.Health(now).AdapterCompatible {
		t.Fatal("unknown real version still injected", w.Body.String())
	}
}
