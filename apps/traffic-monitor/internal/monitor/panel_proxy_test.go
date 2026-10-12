package monitor

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPanelModuleRetryIsBoundedAndReadOnly(t *testing.T) {
	for _, tc := range []struct {
		method, path           string
		status, failures, want int
	}{
		{"GET", "/secret/assets/ClientQrModal-test.js", 503, 1, 2},
		{"GET", "/secret/assets/ClientQrModal-test.js", 502, 10, 3},
		{"GET", "/secret/assets/missing.js", 404, 10, 1},
		{"GET", "/secret/assets/private.js", 401, 10, 1},
		{"POST", "/secret/assets/chunk.js", 503, 1, 1},
		{"GET", "/secret/panel/api/server/status", 503, 1, 1},
	} {
		t.Run(fmt.Sprintf("%s_%s_%d", tc.method, tc.path, tc.status), func(t *testing.T) {
			calls := 0
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls <= tc.failures {
					w.WriteHeader(tc.status)
					return
				}
				w.Header().Set("Content-Type", "text/javascript")
				io.WriteString(w, "export default 1;")
			}))
			defer up.Close()
			tr := &panelAssetTransport{base: http.DefaultTransport, prefix: "/secret/assets/"}
			r, _ := http.NewRequest(tc.method, up.URL+tc.path, nil)
			res, e := tr.RoundTrip(r)
			must(t, e)
			defer res.Body.Close()
			if calls != tc.want {
				t.Fatalf("calls %d want %d", calls, tc.want)
			}
		})
	}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer up.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "GET", up.URL+"/assets/chunk.js", nil)
	_, err := (&panelAssetTransport{base: http.DefaultTransport, prefix: "/assets/"}).RoundTrip(r)
	if err != context.DeadlineExceeded {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestProxyPreservesModulesAndInstallsRecoveryBeforeEntry(t *testing.T) {
	s, c := testStore(t, time.Now())
	c.WebBasePath = "/secret/"
	const js = "export default 'qr-modal';"
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	_, _ = io.WriteString(gz, js)
	must(t, gz.Close())
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'nonce-keep'; frame-ancestors 'none'")
		w.Header().Set("X-Frame-Options", "DENY")
		if strings.HasPrefix(r.URL.Path, "/secret/assets/") {
			if r.Header.Get("Accept-Encoding") != "gzip" || r.Header.Get("If-None-Match") != "old-tag" {
				t.Error("asset compression/cache request changed")
			}
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			w.Header().Set("Cache-Control", "max-age=31536000")
			w.Header().Set("ETag", "new-tag")
			if strings.HasSuffix(r.URL.Path, "missing.js") {
				http.Error(w, "missing chunk", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Encoding", "gzip")
			w.Write(compressed.Bytes())
			return
		}
		if r.Header.Get("Accept-Encoding") != "identity" || r.Header.Get("If-None-Match") != "" {
			t.Error("rewritten document must be fresh and uncompressed")
		}
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, `<html><head><script type="module" src="/secret/assets/entry.js"></script><script nonce="keep">window.X_UI_CUR_VER="4.0.0";</script></head><body></body></html>`)
	}))
	defer up.Close()
	c.Upstream = up.URL
	server := NewServer(c, s, NewCollector(c, s))
	for _, path := range []string{"/secret/assets/qr.js", "/secret/assets/missing.js", "/secret/panel/clients"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Accept-Encoding", "gzip")
		r.Header.Set("If-None-Match", "old-tag")
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		if w.Header().Get("X-Frame-Options") != "DENY" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "nonce-keep") {
			t.Fatal("official security headers lost")
		}
		if strings.HasSuffix(path, "missing.js") {
			if w.Code != 404 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("ETag") != "" {
				t.Fatal("a failed chunk retained the upstream long cache lifetime")
			}
		} else if strings.Contains(path, "assets") {
			if !bytes.Equal(w.Body.Bytes(), compressed.Bytes()) || w.Header().Get("Content-Encoding") != "gzip" || w.Header().Get("ETag") != "new-tag" {
				t.Fatal("module content or encoding changed")
			}
		} else {
			body := w.Body.String()
			a := strings.Index(body, `nonce="keep" src="/secret/traffic-monitor/panel-recovery.js"`)
			b := strings.Index(body, `type="module"`)
			if a < 0 || a > b || strings.Contains(body, "adapter.js") || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("recovery injection order or unknown layout handling: %s", body)
			}
		}
	}
}
