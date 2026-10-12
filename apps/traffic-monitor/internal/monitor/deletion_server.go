package monitor

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type pendingDeletion struct {
	Query   Query
	Preview DeletionPreview
	Session [32]byte
	Expires time.Time
}
type deletionResponse struct {
	DeletionPreview
	Token   string `json:"preview_token"`
	Expires string `json:"expires_at"`
}

func sessionDigest(r *http.Request) [32]byte {
	values := []string{}
	for _, c := range r.Cookies() {
		values = append(values, c.Name+"="+c.Value)
	}
	sort.Strings(values)
	return sha256.Sum256([]byte(strings.Join(values, "\n")))
}
func sameOriginWrite(r *http.Request) bool {
	u, e := url.Parse(r.Header.Get("Origin"))
	if e != nil || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if u.Scheme != scheme || !strings.EqualFold(u.Host, r.Host) {
		return false
	}
	site := r.Header.Get("Sec-Fetch-Site")
	return site == "" || site == "same-origin"
}
func (s *Server) historyPreview(w http.ResponseWriter, r *http.Request, now time.Time) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		failure(w, 405, "请使用 GET 获取删除预览")
		return
	}
	if r.URL.Query().Get("start") == "" || r.URL.Query().Get("end") == "" {
		failure(w, 400, "必须指定删除日期范围")
		return
	}
	q, e := parseQuery(r, s.store.loc, now)
	if e != nil {
		failure(w, 400, e.Error())
		return
	}
	// Freeze today's end to a displayed minute boundary. Do not accidentally
	// suppress the remainder of today or allow new rows to escape a deleted bucket.
	end := now.Truncate(time.Minute).Add(time.Minute)
	if q.End.After(end) {
		q.End = end
	}
	if e = validateDeletion(q); e != nil {
		failure(w, 400, e.Error())
		return
	}
	p, e := s.store.PreviewDeletion(q)
	if e != nil {
		failure(w, 503, "无法预览监测历史")
		return
	}
	var b [32]byte
	_, _ = rand.Read(b[:])
	token := base64.RawURLEncoding.EncodeToString(b[:])
	expires := now.Add(5 * time.Minute)
	s.deleteMu.Lock()
	defer s.deleteMu.Unlock()
	if s.deletions == nil {
		s.deletions = map[string]pendingDeletion{}
	}
	for key, pending := range s.deletions {
		if !now.Before(pending.Expires) {
			delete(s.deletions, key)
		}
	}
	if len(s.deletions) >= 256 {
		failure(w, 429, "删除预览过多，请稍后重试")
		return
	}
	s.deletions[token] = pendingDeletion{Query: q, Preview: p, Session: sessionDigest(r), Expires: expires}
	success(w, deletionResponse{DeletionPreview: p, Token: token, Expires: stamp(expires)})
}
func (s *Server) historyDelete(w http.ResponseWriter, r *http.Request, now time.Time) {
	if !sameOriginWrite(r) {
		failure(w, 403, "删除请求必须来自监测页面的同一来源")
		return
	}
	content, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || content != "application/json" {
		failure(w, 415, "删除请求必须使用 JSON")
		return
	}
	var body struct {
		Token string `json:"preview_token"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
	decoder.DisallowUnknownFields()
	if e = decoder.Decode(&body); e != nil || body.Token == "" || len(body.Token) > 128 {
		failure(w, 400, "无效删除请求")
		return
	}
	if e = decoder.Decode(&struct{}{}); e != io.EOF {
		failure(w, 400, "无效删除请求")
		return
	}
	if subtle.ConstantTimeCompare([]byte(body.Token), []byte(r.Header.Get("X-Monitor-CSRF"))) != 1 {
		failure(w, 403, "删除请求缺少有效的防伪令牌")
		return
	}
	s.deleteMu.Lock()
	p, ok := s.deletions[body.Token]
	if !ok || !now.Before(p.Expires) {
		delete(s.deletions, body.Token)
		s.deleteMu.Unlock()
		failure(w, 409, "预览已过期，请重新预览")
		return
	}
	if p.Session != sessionDigest(r) {
		s.deleteMu.Unlock()
		failure(w, 403, "预览不属于当前登录会话")
		return
	}
	delete(s.deletions, body.Token) // one use, including stale previews and failed commits
	s.deleteMu.Unlock()
	deleted, e := s.store.DeleteHistory(p.Query, p.Preview, now)
	if errors.Is(e, ErrDeletionChanged) {
		failure(w, 409, "数据已变化，请重新预览后确认")
		return
	}
	if e != nil {
		failure(w, 503, "删除未完成，请重新预览后重试")
		return
	}
	success(w, deleted)
}
