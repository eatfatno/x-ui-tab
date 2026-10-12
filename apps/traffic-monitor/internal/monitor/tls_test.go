package monitor

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTLSConfigAndProxiedSessionCookies(t *testing.T) {
	s, cfg := testStore(t, time.Now())
	for _, tc := range []struct {
		cert, key string
		valid     bool
	}{{"", "", true}, {"/cert.pem", "", false}, {"", "/key.pem", false}, {"cert.pem", "/key.pem", false}, {"/cert.pem", "/key.pem", true}} {
		c := cfg
		c.TLSCertFile, c.TLSKeyFile = tc.cert, tc.key
		if (c.Validate() == nil) != tc.valid {
			t.Fatalf("unexpected TLS path validation for %q / %q", tc.cert, tc.key)
		}
	}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Set-Cookie", "3x-ui=session; Path=/secret/; HttpOnly; SameSite=Lax")
		w.Header().Add("Set-Cookie", "other=value; Secure; Path=/")
		w.WriteHeader(http.StatusOK)
	}))
	defer up.Close()
	cfg.Upstream = up.URL
	cfg.TLSCertFile, cfg.TLSKeyFile = filepath.Join(t.TempDir(), "cert"), filepath.Join(t.TempDir(), "key")
	server := NewServer(cfg, s, NewCollector(cfg, s))
	w := httptest.NewRecorder()
	server.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "https://example.test/login", nil))
	cookies := w.Result().Cookies()
	if len(cookies) != 2 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].Path != "/secret/" || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("HTTPS proxy must preserve original session restrictions and add Secure: %v", cookies)
	}
	if strings.Count(w.Header().Values("Set-Cookie")[1], "Secure") != 1 {
		t.Fatal("existing Secure attribute duplicated")
	}
}
