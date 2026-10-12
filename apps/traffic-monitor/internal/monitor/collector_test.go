package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestParseAccess(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	cases := []struct {
		line, host, protocol, inbound, email string
		port                                 int
	}{
		{"2026/10/08 12:30:22.123456 from 127.0.0.1:4567 accepted tcp:example.org:443 [inbound-12 >> direct] email: alice", "example.org", "tcp", "inbound-12", "alice", 443},
		{"2026/10/08 12:30:22 127.0.0.1:4567 accepted udp:[2001:db8::1]:53 [inbound-2 -> direct] email: bob", "2001:db8::1", "udp", "inbound-2", "bob", 53},
		{"2026/10/08 12:30:22.123 from [::1]:4 rejected tcp:1.1.1.1:443 [inbound-4 -> blocked] reason", "1.1.1.1", "tcp", "inbound-4", "", 443},
	}
	for _, tc := range cases {
		v, e := ParseAccess(tc.line, loc)
		must(t, e)
		if v.Target != tc.host || v.Port != tc.port || v.Protocol != tc.protocol || v.Email != tc.email || v.Inbound != tc.inbound || v.At.UTC().Hour() != 4 {
			t.Errorf("%+v", v)
		}
	}
	for _, line := range []string{"bad", "2026/10/08 12:30:22 from a accepted tcp:x:nope", "2026/10/08 12:30:22 from a accepted tcp:x:999999"} {
		if _, e := ParseAccess(line, loc); e == nil {
			t.Fatal("accepted invalid", line)
		}
	}
}

func accessLine(at time.Time, email, target string) string {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	return fmt.Sprintf("%s from 127.0.0.1:123 accepted tcp:%s:443 [inbound-1 >> direct] email: %s\n", at.In(loc).Format("2006/01/02 15:04:05.000000"), target, email)
}

func TestLogBacklogVisibleUntilNextBatchCatchesUp(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	s, cfg := testStore(t, now.Add(-time.Minute))
	must(t, s.Sample(now.Add(-time.Minute), []Snapshot{snap("a", "alice", 0, 0)}))
	must(t, os.WriteFile(cfg.AccessLogPath, []byte(strings.Repeat(accessLine(now, "alice", "api.telegram.org"), 2001)), 0600))
	c := NewCollector(cfg, s)
	must(t, c.Tail(now))
	if !c.issues["log_backlog"] {
		t.Fatal("batch limit did not report backlog")
	}
	must(t, c.Tail(now.Add(10*time.Second)))
	if c.issues["log_backlog"] {
		t.Fatal("caught-up backlog not cleared")
	}
	p, e := s.Visits(allQuery(now.Add(-time.Minute), now.Add(time.Minute)), now)
	must(t, e)
	if p.Total != 2001 {
		t.Fatal("catch-up lost or duplicated records", p.Total)
	}
}

func TestTailPartialRestartRotateTruncate(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	s, c := testStore(t, now.Add(-time.Minute))
	must(t, s.Sample(now.Add(-time.Minute), []Snapshot{snap("a", "alice", 0, 0)}))
	col := NewCollector(c, s)
	line := accessLine(now, "alice", "first.test")
	must(t, os.WriteFile(c.AccessLogPath, []byte(line[:len(line)-1]), 0600))
	must(t, col.Tail(now))
	p, e := s.Visits(allQuery(now.Add(-time.Minute), now.Add(time.Minute)), now)
	must(t, e)
	if p.Total != 0 {
		t.Fatal("partial committed")
	}
	f, e := os.OpenFile(c.AccessLogPath, os.O_APPEND|os.O_WRONLY, 0600)
	must(t, e)
	_, e = f.WriteString("\n")
	must(t, e)
	f.Close()
	col = NewCollector(c, s)
	must(t, col.Tail(now))
	must(t, col.Tail(now))
	p, e = s.Visits(allQuery(now.Add(-time.Minute), now.Add(time.Minute)), now)
	must(t, e)
	if p.Total != 1 {
		t.Fatal("restart duplicates", p.Total)
	}
	must(t, os.Rename(c.AccessLogPath, c.AccessLogPath+".monitor-1"))
	must(t, os.WriteFile(c.AccessLogPath, []byte(accessLine(now, "alice", "second.test")), 0600))
	must(t, col.Tail(now))
	p, e = s.Visits(allQuery(now.Add(-time.Minute), now.Add(time.Minute)), now)
	must(t, e)
	if p.Total != 2 {
		t.Fatal("rotation lost", p.Total)
	}
	must(t, os.WriteFile(c.AccessLogPath, []byte(accessLine(now, "alice", "third.test")), 0600))
	must(t, col.Tail(now))
	p, e = s.Visits(allQuery(now.Add(-time.Minute), now.Add(time.Minute)), now)
	must(t, e)
	if p.Total != 3 {
		t.Fatal("truncate/regrow detection", p.Total)
	}
}

func TestFetchMinimalProjectionAndNoRedirectTokenLeak(t *testing.T) {
	now := time.Now()
	s, c := testStore(t, now)
	must(t, os.WriteFile(c.APITokenFile, []byte("secret-token\n"), 0600))
	var called int
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		if r.URL.Path != "/base/panel/api/clients/list" || r.Header.Get("Authorization") != "Bearer secret-token" {
			t.Error("wrong request")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "obj": []any{map[string]any{"id": 7, "createdAt": 1234567, "email": "alice", "password": "do-not-store", "inboundIds": []int{2, 1}, "traffic": map[string]any{"up": 50, "down": 100}}}})
	}))
	defer up.Close()
	c.Upstream = up.URL
	c.WebBasePath = "/base/"
	col := NewCollector(c, s)
	snaps, e := col.fetch(context.Background())
	must(t, e)
	if called != 1 || len(snaps) != 1 || snaps[0].ID != "local:7:1234000" || snaps[0].Down != 100 {
		t.Fatal(snaps)
	}
	var leaked bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer redirect.Close()
	col.cfg.Upstream = redirect.URL
	if _, e = col.fetch(context.Background()); e == nil || leaked {
		t.Fatal("redirect followed")
	}
}

func TestRawRetentionAndRotationFailure(t *testing.T) {
	now := time.Now()
	s, c := testStore(t, now.Add(-10*24*time.Hour))
	must(t, os.WriteFile(c.AccessLogPath, []byte(strings.Repeat("x", 5000)), 0600))
	c.LogMaxBytes = 4096
	c.XrayAPIAddress = "127.0.0.1:1"
	col := NewCollector(c, s)
	col.reopen = func(context.Context) error { return fmt.Errorf("offline") }
	if e := col.rotateAndPrune(context.Background(), now); e == nil {
		t.Fatal("should report failure")
	}
	if _, e := os.Stat(c.AccessLogPath); e != nil {
		t.Fatal("failed rotation did not restore path")
	}
	old := filepath.Join(filepath.Dir(c.AccessLogPath), "access.log.monitor-old")
	must(t, os.WriteFile(old, []byte(accessLine(now.Add(-8*24*time.Hour), "a", "old.test")+accessLine(now, "a", "new.test")), 0600))
	must(t, pruneRawLog(old, now.Add(-7*24*time.Hour), col.loc))
	if _, e := os.Stat(old); !os.IsNotExist(e) {
		t.Fatal("expired raw log remains")
	}
}

func TestMetadataTracksRealVersionAndInbound(t *testing.T) {
	now := time.Now()
	s, c := testStore(t, now)
	must(t, os.WriteFile(c.APITokenFile, []byte("secret"), 0600))
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing token")
		}
		switch r.URL.Path {
		case "/panel/api/server/status":
			io.WriteString(w, `{"success":true,"obj":{"panelVersion":"v4.0.0"}}`)
		case "/panel/api/inbounds/list/slim":
			io.WriteString(w, `{"success":true,"obj":[{"id":15,"tag":"custom-https"}]}`)
		default:
			t.Error(r.URL.Path)
		}
	}))
	defer up.Close()
	c.Upstream = up.URL
	col := NewCollector(c, s)
	must(t, col.metadata(context.Background(), now))
	h := col.Health(now)
	if h.PanelVersion != "v4.0.0" || h.AdapterCompatible {
		t.Fatal(h)
	}
	var id int
	must(t, s.db.QueryRow("SELECT inbound_id FROM inbound_aliases WHERE tag='custom-https'").Scan(&id))
	if id != 15 {
		t.Fatal(id)
	}
}

func TestExternalRenameAndOversizedLine(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	s, c := testStore(t, now.Add(-time.Minute))
	must(t, s.Sample(now.Add(-time.Minute), []Snapshot{snap("a", "alice", 0, 0)}))
	col := NewCollector(c, s)
	must(t, os.WriteFile(c.AccessLogPath, []byte(accessLine(now, "alice", "first.test")), 0600))
	must(t, col.Tail(now))
	must(t, os.Rename(c.AccessLogPath, c.AccessLogPath+".1"))
	f, e := os.OpenFile(c.AccessLogPath+".1", os.O_APPEND|os.O_WRONLY, 0600)
	must(t, e)
	_, e = f.WriteString(accessLine(now, "alice", "late.test"))
	must(t, e)
	f.Close()
	must(t, os.WriteFile(c.AccessLogPath, []byte(strings.Repeat("x", 300<<10)+"\n"+accessLine(now, "alice", "new.test")), 0600))
	must(t, col.Tail(now))
	p, e := s.Visits(allQuery(now.Add(-time.Minute), now.Add(time.Minute)), now)
	must(t, e)
	if p.Total != 3 {
		t.Fatal("rotation or oversized line dropped valid rows", p)
	}
}

func TestLoggerReopenWireMethod(t *testing.T) {
	now := time.Now()
	s, c := testStore(t, now)
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	must(t, e)
	server := grpc.NewServer()
	called := make(chan bool, 1)
	server.RegisterService(&grpc.ServiceDesc{ServiceName: "xray.app.log.command.LoggerService", HandlerType: (*interface{})(nil), Methods: []grpc.MethodDesc{{MethodName: "RestartLogger", Handler: func(_ any, ctx context.Context, decode func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
		req := &emptypb.Empty{}
		if e := decode(req); e != nil {
			return nil, e
		}
		called <- true
		return &emptypb.Empty{}, nil
	}}}}, struct{}{})
	go server.Serve(listener)
	defer server.Stop()
	c.XrayAPIAddress = listener.Addr().String()
	col := NewCollector(c, s)
	must(t, col.reopenLogger(context.Background()))
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("logger method not invoked")
	}
}

func TestRetentionIncludesRecognizedExternalLogsAndExpiredActive(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	s, c := testStore(t, now)
	c.XrayAPIAddress = "127.0.0.1:1"
	col := NewCollector(c, s)
	old := []byte(accessLine(now.Add(-8*24*time.Hour), "alice", "old.test"))
	fresh := []byte(accessLine(now, "alice", "fresh.test"))
	must(t, os.WriteFile(c.AccessLogPath, old, 0600))
	for _, suffix := range []string{".1", ".2026-10-01", ".monitor-123"} {
		must(t, os.WriteFile(c.AccessLogPath+suffix, old, 0600))
	}
	for _, suffix := range []string{".1.gz", ".backup", ".monitor-notes"} {
		must(t, os.WriteFile(c.AccessLogPath+suffix, old, 0600))
	}
	must(t, os.WriteFile(c.AccessLogPath+".2", fresh, 0600))
	link := c.AccessLogPath + ".3"
	must(t, os.Symlink(c.AccessLogPath+".backup", link))
	must(t, os.Mkdir(c.AccessLogPath+".4", 0700))
	reopened := 0
	col.reopen = func(context.Context) error { reopened++; return os.WriteFile(c.AccessLogPath, fresh, 0600) }
	must(t, col.rotateAndPrune(context.Background(), now))
	if reopened != 1 {
		t.Fatal("expired existing active file did not rotate", reopened)
	}
	for _, suffix := range []string{".1", ".2026-10-01", ".monitor-123"} {
		if _, e := os.Stat(c.AccessLogPath + suffix); !os.IsNotExist(e) {
			t.Fatal("expired recognized file remains", suffix)
		}
	}
	for _, suffix := range []string{"", ".2", ".1.gz", ".backup", ".monitor-notes", ".3", ".4"} {
		if _, e := os.Lstat(c.AccessLogPath + suffix); e != nil {
			t.Fatal("unowned/fresh file changed", suffix, e)
		}
	}
	paths, e := recognizedRotations(c.AccessLogPath)
	must(t, e)
	if len(paths) != 1 || paths[0] != c.AccessLogPath+".2" {
		t.Fatal("allowlist mismatch", paths)
	}
}
