package monitor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestDeletionSessionCSRFPreviewAndOneUse(t *testing.T) {
	s, c, at, q := deletionFixture(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("collector token used for browser auth")
		}
		if !strings.HasPrefix(r.Header.Get("Cookie"), "session=valid") {
			http.Error(w, "no", 401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"obj":{"panelVersion":"3.9.0"}}`)
	}))
	defer up.Close()
	c.Upstream = up.URL
	c.WebBasePath = "/secret/"
	server := NewServer(c, s, NewCollector(c, s))
	server.now = func() time.Time { return at.Add(20 * time.Second) }
	prefix := "/secret/traffic-monitor/api/v1/"
	query := url.Values{"start": {stamp(q.Start)}, "end": {stamp(q.End)}, "client_id": {"a"}}.Encode()
	call := func(method, path, cookie, origin, csrf, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Cookie", cookie)
		r.Header.Set("Origin", origin)
		r.Header.Set("X-Monitor-CSRF", csrf)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w
	}
	preview := func() string {
		t.Helper()
		w := call("GET", prefix+"history/preview?"+query, "session=valid-a", "", "", "")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var x struct {
			Obj deletionResponse `json:"obj"`
		}
		must(t, json.Unmarshal(w.Body.Bytes(), &x))
		if x.Obj.Total != 300 {
			t.Fatal(x)
		}
		return x.Obj.Token
	}
	if w := call("GET", prefix+"history/preview?"+query, "", "", "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	token := preview()
	body := `{"preview_token":"` + token + `"}`
	for _, tc := range []struct {
		cookie, origin, csrf string
		status               int
	}{
		{"", "http://example.com", token, 401},
		{"session=valid-a", "https://evil.test", token, 403},
		{"session=valid-a", "", token, 403},
		{"session=valid-a", "http://example.com", "", 403},
		{"session=valid-b", "http://example.com", token, 403},
	} {
		if w := call("POST", prefix+"history/delete", tc.cookie, tc.origin, tc.csrf, body); w.Code != tc.status {
			t.Fatal(tc.status, w.Code, w.Body.String())
		}
	}
	if w := call("GET", prefix+"history/delete", "session=valid-a", "", token, body); w.Code != 405 {
		t.Fatal("GET deleted data")
	}
	if w := call("POST", prefix+"history/delete", "session=valid-a", "http://example.com", token, body); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("POST", prefix+"history/delete", "session=valid-a", "http://example.com", token, body); w.Code != 409 {
		t.Fatal("replayed token", w.Code)
	}
	x, e := s.Summary(q, at)
	must(t, e)
	if x.Total != 0 {
		t.Fatal(x)
	}
}

func TestDeletionPreviewExpiryAndConcurrentChange(t *testing.T) {
	s, c, at, q := deletionFixture(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"obj":{}}`)
	}))
	defer up.Close()
	c.Upstream = up.URL
	server := NewServer(c, s, NewCollector(c, s))
	now := at.Add(20 * time.Second)
	server.now = func() time.Time { return now }
	preview := func() string {
		r := httptest.NewRequest("GET", "/traffic-monitor/api/v1/history/preview?"+url.Values{"start": {stamp(q.Start)}, "end": {stamp(q.End)}, "client_id": {"a"}}.Encode(), nil)
		r.Header.Set("Cookie", "session=x")
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var x struct {
			Obj deletionResponse `json:"obj"`
		}
		must(t, json.Unmarshal(w.Body.Bytes(), &x))
		return x.Obj.Token
	}
	remove := func(token string) int {
		r := httptest.NewRequest("POST", "/traffic-monitor/api/v1/history/delete", strings.NewReader(`{"preview_token":"`+token+`"}`))
		r.Header.Set("Cookie", "session=x")
		r.Header.Set("Origin", "http://example.com")
		r.Header.Set("X-Monitor-CSRF", token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w.Code
	}
	token := preview()
	now = now.Add(6 * time.Minute)
	if remove(token) != 409 {
		t.Fatal("expired token accepted")
	}
	token = preview()
	_, e := s.db.Exec("UPDATE traffic SET up=up+1 WHERE client_id='a'")
	must(t, e)
	if remove(token) != 409 {
		t.Fatal("stale preview accepted")
	}
	x, e := s.Summary(q, now)
	must(t, e)
	if x.Total != 301 || x.VisitCount != 1 {
		t.Fatal("stale deletion mutated rows", x)
	}
}
