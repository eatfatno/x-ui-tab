package monitor

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"
)

// Only this explicit browser action can write to the official panel. It uses
// the current browser session and official CSRF token, never the collector token.
type panelBrowser struct {
	client *http.Client
	base   string
	w      http.ResponseWriter
}

func (s *Server) panelBrowser(w http.ResponseWriter, r *http.Request) *panelBrowser {
	base := strings.TrimRight(s.cfg.Upstream, "/") + s.cfg.WebBasePath
	u, _ := url.Parse(base)
	jar, _ := cookiejar.New(nil)
	jar.SetCookies(u, r.Cookies())
	return &panelBrowser{base: base, w: w, client: &http.Client{Jar: jar, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (p *panelBrowser) call(ctx context.Context, method, route string, body any, csrf string, out any) error {
	var reader io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return e
		}
		reader = bytes.NewReader(b)
	}
	r, e := http.NewRequestWithContext(ctx, method, p.base+route, reader)
	if e != nil {
		return e
	}
	r.Header.Set("Accept", "application/json")
	r.Header.Set("X-Requested-With", "XMLHttpRequest")
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if csrf != "" {
		r.Header.Set("X-CSRF-Token", csrf)
	}
	resp, e := p.client.Do(r)
	if e != nil {
		return errors.New("panel request failed")
	}
	defer resp.Body.Close()
	for _, cookie := range resp.Header.Values("Set-Cookie") {
		p.w.Header().Add("Set-Cookie", cookie)
	}
	if resp.StatusCode != 200 {
		return errors.New("panel request rejected")
	}
	var payload struct {
		Success bool            `json:"success"`
		Obj     json.RawMessage `json:"obj"`
	}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&payload); e != nil || !payload.Success {
		return errors.New("panel request unsuccessful")
	}
	if out != nil {
		return json.Unmarshal(payload.Obj, out)
	}
	return nil
}

type resetClient struct {
	ID         string `json:"id"`
	Email      string `json:"email"`
	InboundIDs []int  `json:"inbound_ids"`
	Enabled    bool   `json:"enabled"`
	Up         int64  `json:"up"`
	Down       int64  `json:"down"`
	hasTraffic bool
}

func (s *Server) resetClients(ctx context.Context, p *panelBrowser) ([]resetClient, error) {
	var records []struct {
		ID         json.Number `json:"id"`
		CreatedAt  int64       `json:"createdAt"`
		Email      string      `json:"email"`
		Enable     bool        `json:"enable"`
		InboundIDs []int       `json:"inboundIds"`
		Traffic    *struct {
			Up   int64 `json:"up"`
			Down int64 `json:"down"`
		} `json:"traffic"`
	}
	if e := p.call(ctx, http.MethodGet, "panel/api/clients/list", nil, "", &records); e != nil {
		return nil, e
	}
	result := []resetClient{}
	for _, v := range records {
		if v.ID == "" || v.Email == "" {
			return nil, errors.New("invalid panel identity")
		}
		id := panelClientID(s.cfg.InstanceID, v.ID.String(), v.CreatedAt)
		x := resetClient{ID: id, Email: v.Email, InboundIDs: v.InboundIDs, Enabled: v.Enable, hasTraffic: v.Traffic != nil}
		if v.Traffic != nil {
			x.Up = v.Traffic.Up
			x.Down = v.Traffic.Down
		}
		result = append(result, x)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func selectResetClients(all []resetClient, q Query) []resetClient {
	result := []resetClient{}
	for _, c := range all {
		if (q.ClientID == "" || c.ID == q.ClientID) && (q.InboundID == 0 || slices.Contains(c.InboundIDs, q.InboundID)) {
			result = append(result, c)
		}
	}
	return result
}
func resetSnapshots(clients []resetClient) []Snapshot {
	result := make([]Snapshot, 0, len(clients))
	for _, c := range clients {
		result = append(result, Snapshot{Client: Client{ID: c.ID, Email: c.Email, InboundIDs: c.InboundIDs, Up: c.Up, Down: c.Down}, HasTraffic: c.hasTraffic})
	}
	return result
}

type panelResetPreview struct {
	Clients  []resetClient `json:"clients"`
	Up       int64         `json:"up"`
	Down     int64         `json:"down"`
	Disabled int           `json:"disabled_count"`
	Token    string        `json:"preview_token"`
	Expires  string        `json:"expires_at"`
}
type pendingPanelReset struct {
	Query   Query
	Clients []resetClient
	Session [32]byte
	Expires time.Time
}

func (s *Server) panelResetPreview(w http.ResponseWriter, r *http.Request, now time.Time) {
	if r.Method != http.MethodGet {
		failure(w, 405, "请使用 GET 获取清零预览")
		return
	}
	for k := range r.URL.Query() {
		if k != "client_id" && k != "inbound_id" {
			failure(w, 400, "累计清零只按客户端或入站范围选择，不按日期或请求目标选择")
			return
		}
	}
	q, e := parseQuery(r, s.store.loc, now)
	if e != nil {
		failure(w, 400, e.Error())
		return
	}
	all, e := s.resetClients(r.Context(), s.panelBrowser(w, r))
	if e != nil {
		failure(w, 503, "无法读取官方当前累计，请稍后重试")
		return
	}
	clients := selectResetClients(all, q)
	if len(clients) == 0 || len(clients) > 1000 {
		failure(w, 400, "请选择 1 至 1000 个现存客户端")
		return
	}
	var b [32]byte
	if _, e = rand.Read(b[:]); e != nil {
		failure(w, 503, "无法创建清零预览")
		return
	}
	token := base64.RawURLEncoding.EncodeToString(b[:])
	expires := now.Add(5 * time.Minute)
	p := panelResetPreview{Clients: clients, Token: token, Expires: stamp(expires)}
	for _, c := range clients {
		p.Up += c.Up
		p.Down += c.Down
		if !c.Enabled {
			p.Disabled++
		}
	}
	s.deleteMu.Lock()
	defer s.deleteMu.Unlock()
	if s.resets == nil {
		s.resets = map[string]pendingPanelReset{}
	}
	for key, v := range s.resets {
		if !now.Before(v.Expires) {
			delete(s.resets, key)
		}
	}
	if len(s.resets) >= 256 {
		failure(w, 429, "清零预览过多，请稍后重试")
		return
	}
	s.resets[token] = pendingPanelReset{Query: q, Clients: clients, Session: sessionDigest(r), Expires: expires}
	success(w, p)
}

// Invalidate before the external write. The durable flag also protects the
// next sample after an HTTP timeout or a process crash between the two systems.
func (s *Store) invalidatePanelBaselines(clients []resetClient, now time.Time) error {
	return s.tx(func(tx *sql.Tx) error {
		for _, c := range clients {
			if _, e := tx.Exec("UPDATE clients SET has_traffic=0 WHERE id=?", c.ID); e != nil {
				return e
			}
			if e := quality(tx, now, now, c.ID, "counter_reset_window"); e != nil {
				return e
			}
		}
		return nil
	})
}

func (s *Server) panelReset(w http.ResponseWriter, r *http.Request, now time.Time) {
	if !sameOriginWrite(r) {
		failure(w, 403, "清零请求必须来自同一来源")
		return
	}
	ct, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || ct != "application/json" {
		failure(w, 415, "请使用 JSON 提交清零")
		return
	}
	var body struct {
		Token string `json:"preview_token"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
	d.DisallowUnknownFields()
	if e = d.Decode(&body); e != nil || body.Token == "" || len(body.Token) > 128 {
		failure(w, 400, "无效清零请求")
		return
	}
	if d.Decode(&struct{}{}) != io.EOF {
		failure(w, 400, "无效清零请求")
		return
	}
	if subtle.ConstantTimeCompare([]byte(body.Token), []byte(r.Header.Get("X-Monitor-CSRF"))) != 1 {
		failure(w, 403, "无效防伪令牌")
		return
	}
	s.deleteMu.Lock()
	p, ok := s.resets[body.Token]
	if !ok || !now.Before(p.Expires) {
		delete(s.resets, body.Token)
		s.deleteMu.Unlock()
		failure(w, 409, "清零预览已过期，请重新预览")
		return
	}
	if p.Session != sessionDigest(r) {
		s.deleteMu.Unlock()
		failure(w, 403, "清零预览不属于当前会话")
		return
	}
	delete(s.resets, body.Token)
	s.deleteMu.Unlock()
	s.collector.controlMu.Lock()
	defer s.collector.controlMu.Unlock()
	defer func() { s.collector.lastControl = s.now() }()
	upstream := s.panelBrowser(w, r)
	all, e := s.resetClients(r.Context(), upstream)
	if e != nil {
		failure(w, 503, "无法核对当前客户端，尚未提交清零")
		return
	}
	current := selectResetClients(all, p.Query)
	if !slices.EqualFunc(current, p.Clients, func(a, b resetClient) bool { return a.ID == b.ID && a.Email == b.Email && a.Enabled == b.Enabled }) {
		failure(w, 409, "客户端或启用状态已变化，请重新预览")
		return
	}
	var csrf string
	if e = upstream.call(r.Context(), http.MethodGet, "csrf-token", nil, "", &csrf); e != nil || csrf == "" {
		failure(w, 503, "无法取得官方防伪令牌，尚未提交清零")
		return
	}
	if e = s.store.Sample(s.now(), resetSnapshots(all)); e != nil {
		failure(w, 503, "无法同步清零前用量，尚未提交清零")
		return
	}
	if e = s.store.invalidatePanelBaselines(current, s.now()); e != nil {
		failure(w, 503, "无法保存采样基线，尚未提交清零")
		return
	}
	s.collector.invalidateLiveBaselines(current)
	emails := make([]string, len(current))
	for i, c := range current {
		emails[i] = c.Email
	}
	// Once the action is submitted, complete reconciliation even if the browser
	// closes its modal. There is no cross-database transaction with 3x-ui.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 25*time.Second)
	defer cancel()
	var result struct {
		Affected int `json:"affected"`
	}
	if e = upstream.call(ctx, http.MethodPost, "panel/api/clients/bulkResetTraffic", map[string]any{"emails": emails}, csrf, &result); e != nil {
		failure(w, 502, "官方清零结果未确认，请刷新核对累计后再操作；采样将在下次同步时重建基线")
		return
	}
	after, e := s.resetClients(ctx, upstream)
	synced := e == nil
	if synced {
		synced = s.store.Sample(s.now(), resetSnapshots(after)) == nil
	}
	success(w, map[string]any{"affected": result.Affected, "client_count": len(current), "synced": synced})
}
