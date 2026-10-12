package monitor

import (
	"bytes"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Server struct {
	cfg       Config
	store     *Store
	collector *Collector
	authHTTP  *http.Client
	proxy     *httputil.ReverseProxy
	now       func() time.Time
	deleteMu  sync.Mutex
	deletions map[string]pendingDeletion
	resets    map[string]pendingPanelReset
}

var panelVersionRE = regexp.MustCompile(`window\.X_UI_CUR_VER="([0-9A-Za-z.\-]+)"`)
var nonceRE = regexp.MustCompile(`'nonce-([^']+)'`)

func NewServer(c Config, s *Store, col *Collector) *Server {
	u, _ := url.Parse(c.Upstream)
	p := httputil.NewSingleHostReverseProxy(u)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	p.Transport = &panelAssetTransport{base: transport, prefix: c.WebBasePath + "assets/"}
	original := p.Director
	p.Director = func(r *http.Request) {
		original(r)
		if panelDocument(c, r.URL.Path) {
			// Only documents are rewritten. Keep compression and cache validators
			// intact for upstream scripts, styles, APIs and downloads.
			r.Header.Set("Accept-Encoding", "identity")
			r.Header.Del("If-None-Match")
			r.Header.Del("If-Modified-Since")
		}
	}
	p.ModifyResponse = func(r *http.Response) error {
		// The upstream is local HTTP when this service terminates public TLS.
		// Preserve every original cookie attribute and require HTTPS in browsers.
		if c.TLSCertFile != "" {
			values := r.Header.Values("Set-Cookie")
			for i, value := range values {
				secure := false
				for _, attr := range strings.Split(value, ";")[1:] {
					secure = secure || strings.EqualFold(strings.TrimSpace(attr), "secure")
				}
				if !secure {
					values[i] = value + "; Secure"
				}
			}
			if len(values) > 0 {
				r.Header["Set-Cookie"] = values
			}
		}
		// The upstream assets middleware adds a one-year lifetime even to 404s.
		// A failed chunk must not remain stuck in the browser cache after recovery.
		if r.StatusCode >= 400 && strings.HasPrefix(r.Request.URL.Path, c.WebBasePath+"assets/") {
			r.Header.Set("Cache-Control", "no-store")
			r.Header.Del("Expires")
			r.Header.Del("ETag")
			r.Header.Del("Last-Modified")
		}
		// The script stays dormant on login until the verified panel shell exists.
		if r.StatusCode != 200 || !panelDocument(c, r.Request.URL.Path) || !strings.Contains(r.Header.Get("Content-Type"), "text/html") {
			return nil
		}
		if r.Header.Get("Content-Encoding") != "" {
			return nil
		}
		b, e := io.ReadAll(io.LimitReader(r.Body, (4<<20)+1))
		if e != nil {
			return e
		}
		if len(b) > 4<<20 {
			r.Body = &prefixedBody{Reader: io.MultiReader(bytes.NewReader(b), r.Body), Closer: r.Body}
			return nil
		}
		r.Body.Close()
		if m := panelVersionRE.FindSubmatch(b); m != nil {
			col.setVersion(string(m[1]))
		}
		nonce := ""
		if m := nonceRE.FindStringSubmatch(r.Header.Get("Content-Security-Policy")); m != nil {
			nonce = ` nonce="` + html.EscapeString(m[1]) + `"`
		}
		// Install before the official module entry, including on unfamiliar panel
		// versions. This recovery script has no dependency on the panel layout.
		recovery := []byte(`<script` + nonce + ` src="` + html.EscapeString(c.Prefix()+"panel-recovery.js") + `"></script>`)
		if !bytes.Contains(b, recovery) {
			if idx := bytes.Index(bytes.ToLower(b), []byte("<head>")); idx >= 0 {
				idx += len("<head>")
				b = append(append(append([]byte{}, b[:idx]...), recovery...), b[idx:]...)
			}
		}
		tag := []byte(`<script defer` + nonce + ` src="` + html.EscapeString(c.Prefix()+"adapter.js") + `"></script>`)
		if compatible(col.Version()) && !bytes.Contains(b, tag) {
			if idx := bytes.LastIndex(bytes.ToLower(b), []byte("</body>")); idx >= 0 {
				b = append(append(append([]byte{}, b[:idx]...), tag...), b[idx:]...)
			}
		}
		r.Body = io.NopCloser(bytes.NewReader(b))
		r.ContentLength = int64(len(b))
		r.Header.Set("Content-Length", strconv.Itoa(len(b)))
		r.Header.Del("ETag")
		r.Header.Del("Last-Modified")
		r.Header.Set("Cache-Control", "no-store")
		return nil
	}
	p.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		failure(w, http.StatusBadGateway, "官方面板暂时不可用")
	}
	return &Server{cfg: c, store: s, collector: col, proxy: p, authHTTP: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, now: time.Now}
}

type prefixedBody struct {
	io.Reader
	io.Closer
}

func (s *Server) sessionOK(r *http.Request) (bool, error) {
	if r.Header.Get("Cookie") == "" {
		return false, nil
	}
	req, e := http.NewRequestWithContext(r.Context(), http.MethodGet, strings.TrimRight(s.cfg.Upstream, "/")+s.cfg.WebBasePath+"panel/api/server/status", nil)
	if e != nil {
		return false, e
	}
	req.Header.Set("Cookie", r.Header.Get("Cookie"))
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Accept", "application/json")
	req.Host = r.Host
	// Deliberately copy neither Authorization nor any client-provided forwarding
	// header. Browser access must prove a session, never the collector token.
	resp, e := s.authHTTP.Do(req)
	if e != nil {
		return false, e
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return false, errors.New("panel authentication unavailable")
	}
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "application/json") {
		return false, nil
	}
	var env struct {
		Success bool            `json:"success"`
		Obj     json.RawMessage `json:"obj"`
	}
	e = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&env)
	valid := e == nil && env.Success && len(env.Obj) > 0 && string(env.Obj) != "null"
	if valid {
		var status struct {
			PanelVersion string `json:"panelVersion"`
		}
		if json.Unmarshal(env.Obj, &status) == nil {
			s.collector.setVersion(status.PanelVersion)
		}
	}
	return valid, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	prefix := s.cfg.Prefix()
	if r.URL.Path == strings.TrimSuffix(prefix, "/") {
		http.Redirect(w, r, prefix, http.StatusTemporaryRedirect)
		return
	}
	if !strings.HasPrefix(r.URL.Path, prefix) {
		s.proxy.ServeHTTP(w, r)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; object-src 'none'; frame-ancestors 'self'; base-uri 'self'; form-action 'self'")
	rel := strings.TrimPrefix(r.URL.Path, prefix)
	isDelete := rel == "api/v1/history/delete" || rel == "api/v1/panel/reset"
	if (isDelete && r.Method != http.MethodPost) || (!isDelete && r.Method != http.MethodGet && r.Method != http.MethodHead) {
		allow := "GET, HEAD"
		if isDelete {
			allow = "POST"
		}
		w.Header().Set("Allow", allow)
		failure(w, 405, "此接口不支持该请求方法")
		return
	}
	isAPI := strings.HasPrefix(rel, "api/")
	isAsset := rel == "adapter.js" || rel == "panel-recovery.js" || strings.HasPrefix(rel, "assets/")
	if !isAsset {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Vary", "Cookie")
		ok, err := s.sessionOK(r)
		if err != nil {
			failure(w, 503, "官方面板暂时不可用，无法验证会话，请稍后重试")
			return
		}
		if !ok {
			if isAPI {
				failure(w, 401, "面板会话已失效，请重新登录")
			} else {
				http.Redirect(w, r, s.cfg.WebBasePath, 303)
			}
			return
		}
	}
	if isAPI {
		s.api(w, r, strings.TrimPrefix(rel, "api/v1/"))
		return
	}
	if rel == "" || rel == "index.html" {
		http.ServeFile(w, r, filepath.Join(s.cfg.WebDir, "index.html"))
		return
	}
	if isAsset && path.Clean(rel) == rel && !strings.Contains(rel, "\\") {
		if rel == "adapter.js" || rel == "panel-recovery.js" {
			w.Header().Set("Cache-Control", "no-cache")
		}
		file := filepath.Join(s.cfg.WebDir, filepath.FromSlash(rel))
		st, e := os.Stat(file)
		if e == nil && st.Mode().IsRegular() {
			http.ServeFile(w, r, file)
			return
		}
	}
	http.NotFound(w, r)
}

func parseQuery(r *http.Request, loc *time.Location, now time.Time) (Query, error) {
	v := r.URL.Query()
	d := now.In(loc)
	q := Query{Start: time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc), End: time.Date(d.Year(), d.Month(), d.Day()+1, 0, 0, 0, 0, loc), Page: 1, PageSize: 50, ClientID: v.Get("client_id"), Target: v.Get("target")}
	var e error
	if str := v.Get("start"); str != "" {
		q.Start, e = time.Parse(time.RFC3339Nano, str)
		if e != nil {
			return q, errors.New("start 必须为含时区的 ISO 8601 时间")
		}
	}
	if str := v.Get("end"); str != "" {
		q.End, e = time.Parse(time.RFC3339Nano, str)
		if e != nil {
			return q, errors.New("end 必须为含时区的 ISO 8601 时间")
		}
	}
	if !q.End.After(q.Start) || q.End.Sub(q.Start) > 35*24*time.Hour {
		return q, errors.New("查询时间范围必须为 0 至 35 天")
	}
	if str := v.Get("inbound_id"); str != "" {
		q.InboundID, e = strconv.Atoi(str)
		if e != nil || q.InboundID < 1 {
			return q, errors.New("inbound_id 必须为正整数")
		}
	}
	if str := v.Get("page"); str != "" {
		q.Page, e = strconv.Atoi(str)
		if e != nil || q.Page < 1 || q.Page > 1000000 {
			return q, errors.New("无效页码")
		}
	}
	if str := v.Get("page_size"); str != "" {
		q.PageSize, e = strconv.Atoi(str)
		if e != nil || q.PageSize < 1 || q.PageSize > 200 {
			return q, errors.New("page_size 必须为 1 至 200")
		}
	}
	if len(q.ClientID) > 256 || len(q.Target) > 512 {
		return q, errors.New("筛选值过长")
	}
	return q, nil
}
func writeJSON(w http.ResponseWriter, status int, obj any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(obj)
}
func success(w http.ResponseWriter, obj any) {
	writeJSON(w, 200, map[string]any{"success": true, "obj": obj})
}
func failure(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"success": false, "msg": msg})
}

func (s *Server) api(w http.ResponseWriter, r *http.Request, route string) {
	now := s.now()
	if route == "realtime" {
		for key := range r.URL.Query() {
			if key != "client_id" && key != "inbound_id" {
				failure(w, 400, "实时状态只按客户端和入站筛选，不使用历史日期或请求目标条件")
				return
			}
		}
		q, err := parseQuery(r, s.store.loc, now)
		if err != nil {
			failure(w, 400, err.Error())
			return
		}
		success(w, s.collector.Realtime(now, q.ClientID, q.InboundID))
		return
	}
	if route == "panel/reset-preview" {
		s.panelResetPreview(w, r, now)
		return
	}
	if route == "panel/reset" {
		s.panelReset(w, r, now)
		return
	}
	if route == "history/preview" {
		s.historyPreview(w, r, now)
		return
	}
	if route == "history/delete" {
		s.historyDelete(w, r, now)
		return
	}
	if route == "rules" {
		success(w, map[string]any{"source": domainRules.Source, "commit": domainRules.Commit, "service_count": domainRules.ServiceCount, "rule_count": domainRules.RuleCount, "ip_rule_count": domainRules.IPRuleCount, "app_rule_commit": domainRules.AppRuleCommit})
		return
	}
	if route == "health" {
		success(w, s.collector.Health(now))
		return
	}
	if route == "clients" {
		x, e := s.store.Clients(now)
		if e != nil {
			failure(w, 503, "客户端数据暂时不可用")
			return
		}
		success(w, x)
		return
	}
	q, e := parseQuery(r, s.store.loc, now)
	if e != nil {
		failure(w, 400, e.Error())
		return
	}
	var obj any
	switch route {
	case "summary":
		obj, e = s.store.Summary(q, now)
	case "traffic":
		obj, e = s.store.Traffic(q, now)
	case "visits":
		obj, e = s.store.Visits(q, now)
	case "targets":
		obj, e = s.store.Targets(q, now)
	default:
		http.NotFound(w, r)
		return
	}
	if e != nil {
		failure(w, 503, "查询暂时不可用")
		return
	}
	success(w, obj)
}
