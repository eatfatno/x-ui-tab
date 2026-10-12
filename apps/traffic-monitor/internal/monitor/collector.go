package monitor

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
)

type Collector struct {
	cfg           Config
	store         *Store
	http          *http.Client
	mu            sync.RWMutex
	controlMu     sync.Mutex
	lastControl   time.Time
	live          LiveSnapshot
	liveBaselines map[string]Snapshot
	issues        map[string]bool
	loc           *time.Location
	lastPrune     time.Time
	panelVersion  string
	reopen        func(context.Context) error
}

func NewCollector(cfg Config, s *Store) *Collector {
	loc, _ := time.LoadLocation(cfg.Timezone)
	c := &Collector{cfg: cfg, store: s, http: &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, issues: map[string]bool{}, loc: loc}
	c.reopen = c.reopenLogger
	return c
}
func (c *Collector) issue(key string, yes bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if yes {
		c.issues[key] = true
	} else {
		delete(c.issues, key)
	}
}
func (c *Collector) Run(ctx context.Context) {
	c.Cycle(ctx, time.Now())
	timer := time.NewTicker(c.cfg.Interval())
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-timer.C:
			c.Cycle(ctx, now)
		}
	}
}
func (c *Collector) Cycle(ctx context.Context, now time.Time) {
	c.controlMu.Lock()
	defer c.controlMu.Unlock()
	if now.Before(c.lastControl) {
		now = time.Now()
	}
	e := c.metadata(ctx, now)
	c.issue("panel_metadata_failed", e != nil)
	snaps, e := c.fetch(ctx)
	if e == nil {
		e = c.store.Sample(now, snaps)
	}
	c.issue("panel_collection_failed", e != nil)
	if e == nil {
		online, onlineErr := c.fetchOnline(ctx)
		c.issue("online_collection_failed", onlineErr != nil)
		c.updateRealtime(now, snaps, online, onlineErr == nil)
	} else {
		c.realtimeUnavailable()
	}
	e = c.Tail(now)
	c.issue("access_log_unavailable", e != nil)
	if now.Sub(c.lastPrune) >= time.Minute {
		e = c.store.Prune(now)
		c.issue("database_cleanup_failed", e != nil)
		e = c.rotateAndPrune(ctx, now)
		c.issue("log_rotation_failed", e != nil)
		c.lastPrune = now
	}
}

func (c *Collector) Version() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.panelVersion != "" {
		return c.panelVersion
	}
	return c.cfg.PanelVersion
}
func (c *Collector) setVersion(v string) {
	if v == "" {
		return
	}
	c.mu.Lock()
	c.panelVersion = v
	c.mu.Unlock()
}
func (c *Collector) readAPI(ctx context.Context, route string, out any) error {
	// Callers use literal routes only; this helper is never exposed through HTTP.
	token, e := os.ReadFile(c.cfg.APITokenFile)
	if e != nil {
		return e
	}
	method := http.MethodGet
	if route == "clients/onlines" {
		method = http.MethodPost
	}
	r, e := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.cfg.Upstream, "/")+c.cfg.WebBasePath+"panel/api/"+route, nil)
	if e != nil {
		return e
	}
	r.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	resp, e := c.http.Do(r)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New("metadata request failed")
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(out)
}
func (c *Collector) metadata(ctx context.Context, now time.Time) error {
	var status struct {
		Success bool `json:"success"`
		Obj     struct {
			PanelVersion string `json:"panelVersion"`
		} `json:"obj"`
	}
	if e := c.readAPI(ctx, "server/status", &status); e != nil {
		return e
	}
	if !status.Success || status.Obj.PanelVersion == "" {
		return errors.New("panel version unavailable")
	}
	c.setVersion(status.Obj.PanelVersion)
	var inbounds struct {
		Success bool `json:"success"`
		Obj     []struct {
			ID  int    `json:"id"`
			Tag string `json:"tag"`
		} `json:"obj"`
	}
	if e := c.readAPI(ctx, "inbounds/list/slim", &inbounds); e != nil {
		return e
	}
	if !inbounds.Success {
		return errors.New("inbound mapping unavailable")
	}
	tags := map[string]int{}
	for _, ib := range inbounds.Obj {
		if ib.Tag != "" && ib.ID > 0 {
			tags[ib.Tag] = ib.ID
		}
	}
	return c.store.SyncInbounds(now, tags)
}

func (c *Collector) fetch(ctx context.Context) ([]Snapshot, error) {
	token, e := os.ReadFile(c.cfg.APITokenFile)
	if e != nil {
		return nil, errors.New("collector token unavailable")
	}
	tok := strings.TrimSpace(string(token))
	if tok == "" {
		return nil, errors.New("collector token empty")
	}
	r, e := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.cfg.Upstream, "/")+c.cfg.WebBasePath+"panel/api/clients/list", nil)
	if e != nil {
		return nil, e
	}
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Accept", "application/json")
	resp, e := c.http.Do(r)
	if e != nil {
		return nil, errors.New("panel request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, errors.New("panel client list unavailable")
	}
	var envelope struct {
		Success bool `json:"success"`
		Obj     []struct {
			ID         json.Number `json:"id"`
			Email      string      `json:"email"`
			CreatedAt  int64       `json:"createdAt"`
			InboundIDs []int       `json:"inboundIds"`
			Traffic    *struct {
				Up   int64 `json:"up"`
				Down int64 `json:"down"`
			} `json:"traffic"`
		} `json:"obj"`
	}
	d := json.NewDecoder(io.LimitReader(resp.Body, 32<<20))
	d.UseNumber()
	if e = d.Decode(&envelope); e != nil || !envelope.Success {
		return nil, errors.New("panel client list invalid")
	}
	out := make([]Snapshot, 0, len(envelope.Obj))
	for _, v := range envelope.Obj {
		if v.ID == "" || v.Email == "" {
			return nil, errors.New("panel identity missing")
		}
		sort.Ints(v.InboundIDs)
		id := panelClientID(c.cfg.InstanceID, v.ID.String(), v.CreatedAt)
		s := Snapshot{Client: Client{ID: id, Email: v.Email, InboundIDs: v.InboundIDs}, HasTraffic: v.Traffic != nil}
		if v.Traffic != nil {
			s.Up = v.Traffic.Up
			s.Down = v.Traffic.Down
		}
		out = append(out, s)
	}
	return out, nil
}

var accessRE = regexp.MustCompile(`^(\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}(?:\.\d+)?)\s+(?:from\s+)?\S+\s+(accepted|rejected)\s+(\S+)(.*)$`)
var detourRE = regexp.MustCompile(`\[([^\]]+)\]`)
var inboundRE = regexp.MustCompile(`(?:^|[^a-zA-Z])inbound-(\d+)(?:$|[^0-9])`)

func internalVisit(v Visit) bool {
	if v.Email != "" || v.Inbound != v.Outbound || (v.Inbound != "api" && v.Inbound != "metrics") {
		return false
	}
	ip := net.ParseIP(v.Target)
	return v.Target == "localhost" || (ip != nil && ip.IsLoopback())
}

func ParseAccess(line string, loc *time.Location) (Visit, error) {
	m := accessRE.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return Visit{}, errors.New("unrecognized access line")
	}
	at, e := time.ParseInLocation("2006/01/02 15:04:05.999999999", m[1], loc)
	if e != nil {
		return Visit{}, e
	}
	v := Visit{At: at, Time: stamp(at), Status: m[2]}
	endpoint := m[3]
	if strings.HasPrefix(endpoint, "tcp:") {
		v.Protocol = "tcp"
		endpoint = strings.TrimPrefix(endpoint, "tcp:")
	} else if strings.HasPrefix(endpoint, "udp:") {
		v.Protocol = "udp"
		endpoint = strings.TrimPrefix(endpoint, "udp:")
	} else {
		v.Protocol = "unknown"
	}
	host, port, e := net.SplitHostPort(endpoint)
	if e != nil {
		return v, errors.New("invalid target endpoint")
	}
	v.Target = host
	v.Port, e = strconv.Atoi(port)
	if e != nil || v.Port < 0 || v.Port > 65535 {
		return v, errors.New("invalid target port")
	}
	tail := m[4]
	if pos := strings.LastIndex(tail, "email: "); pos >= 0 {
		v.Email = strings.TrimSpace(tail[pos+7:])
		tail = tail[:pos]
	}
	if d := detourRE.FindStringSubmatch(tail); d != nil {
		for _, arrow := range []string{" -> ", " >> ", " => "} {
			if a, b, ok := strings.Cut(d[1], arrow); ok {
				v.Inbound = strings.TrimSpace(a)
				v.Outbound = strings.TrimSpace(b)
				break
			}
		}
		if v.Inbound == "" {
			v.Inbound = strings.TrimSpace(d[1])
		}
	}
	if n := inboundRE.FindStringSubmatch(v.Inbound); n != nil {
		v.InboundID, _ = strconv.Atoi(n[1])
	}
	return v, nil
}

func anchor(f *os.File, offset int64) (string, error) {
	if offset == 0 {
		return "", nil
	}
	n := int64(128)
	if offset < n {
		n = offset
	}
	b := make([]byte, n)
	_, e := f.ReadAt(b, offset-n)
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

// Completed lines only advance the transaction checkpoint. A partial line is
// persisted in the original log and reread on the next cycle, including restart.
func (c *Collector) tailFile(path string, now time.Time) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return e
	}
	id := fileIdentity(st)
	cp, e := c.store.Checkpoint(id)
	if e != nil {
		return e
	}
	if cp.Offset > st.Size() {
		cp.Offset = 0
		cp.Anchor = ""
		if e = c.store.RecordIssue(now, "log_truncated"); e != nil {
			return e
		}
	} else if cp.Offset > 0 {
		a, e := anchor(f, cp.Offset)
		if e != nil {
			return e
		}
		if a != cp.Anchor {
			cp.Offset = 0
			cp.Anchor = ""
			if e = c.store.RecordIssue(now, "log_truncated"); e != nil {
				return e
			}
		}
	}
	if _, e = f.Seek(cp.Offset, io.SeekStart); e != nil {
		return e
	}
	reader := bufio.NewReaderSize(f, 64<<10)
	visits := []Visit{}
	offset := cp.Offset
	malformed := false
	for len(visits) < 2000 && offset-cp.Offset < 4<<20 {
		line, consumed, oversized, e := boundedLine(reader)
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		offset += consumed
		if oversized {
			malformed = true
			continue
		}
		v, e := ParseAccess(line, c.loc)
		if e != nil {
			malformed = true
			continue
		}
		visits = append(visits, v)
	}
	if offset == cp.Offset {
		return nil
	}
	backlog := offset < st.Size() && (len(visits) >= 2000 || offset-cp.Offset >= 4<<20)
	cp.Offset = offset
	cp.Anchor, e = anchor(f, cp.Offset)
	if e != nil {
		return e
	}
	if e = c.store.Ingest(id, cp, visits, now, malformed); e != nil {
		return e
	}
	if backlog {
		c.issue("log_backlog", true)
	}
	c.issue("log_parse_error", malformed)
	return nil
}

func boundedLine(r *bufio.Reader) (string, int64, bool, error) {
	var b []byte
	var n int64
	oversized := false
	for {
		chunk, e := r.ReadSlice('\n')
		n += int64(len(chunk))
		if n <= 256<<10 {
			b = append(b, chunk...)
		} else {
			oversized = true
		}
		if e == bufio.ErrBufferFull {
			continue
		}
		return string(b), n, oversized, e
	}
}

func (c *Collector) Tail(now time.Time) error {
	c.issue("log_backlog", false)
	paths, e := recognizedRotations(c.cfg.AccessLogPath)
	if e != nil {
		return e
	}
	paths = append(paths, c.cfg.AccessLogPath)
	for _, p := range paths {
		if e = c.tailFile(p, now); e != nil {
			return e
		}
	}
	return nil
}

var rotationSuffix = regexp.MustCompile(`^(?:monitor-[0-9]+|[0-9][0-9.\-]*)$`)

// One allowlist governs both ingestion and retention. Compressed logs, unrelated
// suffixes, directories and symlinks belong to their external owner.
func recognizedRotations(accessPath string) ([]string, error) {
	all, e := filepath.Glob(accessPath + ".*")
	if e != nil {
		return nil, e
	}
	paths := []string{}
	for _, p := range all {
		if !rotationSuffix.MatchString(strings.TrimPrefix(p, accessPath+".")) {
			continue
		}
		info, e := os.Lstat(p)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return nil, e
		}
		if info.Mode().IsRegular() {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func (c *Collector) reopenLogger(ctx context.Context) error {
	if c.cfg.XrayAPIAddress == "" {
		return errors.New("log reopen API is not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, e := grpc.NewClient(c.cfg.XrayAPIAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if e != nil {
		return e
	}
	defer conn.Close()
	return conn.Invoke(ctx, "/xray.app.log.command.LoggerService/RestartLogger", &emptypb.Empty{}, &emptypb.Empty{})
}

func (c *Collector) rotateAndPrune(ctx context.Context, now time.Time) error {
	st, e := os.Stat(c.cfg.AccessLogPath)
	if e != nil {
		return e
	}
	first := c.store.Meta("log_opened_at")
	opened, _ := time.Parse(time.RFC3339Nano, first)
	if opened.IsZero() {
		opened = now
		if e = c.store.tx(func(tx *sql.Tx) error { return setMeta(tx, "log_opened_at", stamp(now)) }); e != nil {
			return e
		}
	}
	cut := now.AddDate(0, 0, -c.cfg.DetailDays)
	expired, e := rawLogExpired(c.cfg.AccessLogPath, cut, c.loc)
	if e != nil {
		return e
	}
	// A daily rotation makes the oldest retained active file bounded even on quiet nodes.
	// Existing logs may already contain expired records on the first startup.
	if expired || st.Size() >= c.cfg.LogMaxBytes || now.Sub(opened) >= 24*time.Hour {
		if c.cfg.XrayAPIAddress == "" {
			return errors.New("log rotation requires LoggerService")
		}
		rotated := fmt.Sprintf("%s.monitor-%020d", c.cfg.AccessLogPath, now.UnixNano())
		if e = os.Rename(c.cfg.AccessLogPath, rotated); e != nil {
			return e
		}
		if e = c.reopen(ctx); e != nil {
			_ = os.Rename(rotated, c.cfg.AccessLogPath)
			return e
		}
		if e = c.store.tx(func(tx *sql.Tx) error { return setMeta(tx, "log_opened_at", stamp(now)) }); e != nil {
			return e
		}
	}
	paths, e := recognizedRotations(c.cfg.AccessLogPath)
	if e != nil {
		return e
	}
	for _, p := range paths {
		if e = pruneRawLog(p, cut, c.loc); e != nil {
			return e
		}
	}
	return nil
}

// Delete a closed file when its earliest record expires. The database retains
// the full detail window; raw logs may have up to one day less history. Never
// rewrite retained files, which would invalidate their transaction checkpoints.
func pruneRawLog(path string, cut time.Time, loc *time.Location) error {
	expired, e := rawLogExpired(path, cut, loc)
	if e != nil {
		return e
	}
	if expired {
		return os.Remove(path)
	}
	return nil
}

func rawLogExpired(path string, cut time.Time, loc *time.Location) (bool, error) {
	f, e := os.Open(path)
	if e != nil {
		return false, e
	}
	st, e := f.Stat()
	if e != nil {
		f.Close()
		return false, e
	}
	remove := st.ModTime().Before(cut)
	r := bufio.NewScanner(io.LimitReader(f, 1<<20))
	r.Buffer(make([]byte, 4096), 256<<10)
	for r.Scan() {
		v, e := ParseAccess(r.Text(), loc)
		if e == nil {
			remove = remove || v.At.Before(cut)
			break
		}
	}
	f.Close()
	return remove, nil
}

func (c *Collector) Health(now time.Time) Health {
	c.mu.RLock()
	issues := make([]string, 0, len(c.issues))
	for k := range c.issues {
		issues = append(issues, k)
	}
	c.mu.RUnlock()
	last := c.store.Meta("last_sample_at")
	t, _ := time.Parse(time.RFC3339Nano, last)
	if t.IsZero() || now.Sub(t) > c.cfg.Interval()*3 {
		issues = append(issues, "sample_stale")
	}
	version := c.Version()
	if !compatible(version) {
		issues = append(issues, "adapter_unsupported")
	}
	sort.Strings(issues)
	status := "ok"
	if len(issues) > 0 {
		status = "degraded"
	}
	return Health{Status: status, PanelVersion: version, AdapterCompatible: compatible(version), LastSampleAt: last, LastLogAt: c.store.Meta("last_log_at"), MonitorStartedAt: stamp(c.store.started), DetailDays: c.cfg.DetailDays, SummaryDays: c.cfg.SummaryDays, Timezone: c.cfg.Timezone, Issues: issues}
}
