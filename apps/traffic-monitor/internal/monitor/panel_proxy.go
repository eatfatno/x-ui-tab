package monitor

import (
	"net/http"
	"strings"
	"time"
)

func panelDocument(c Config, path string) bool {
	panel := c.WebBasePath + "panel"
	return path == c.WebBasePath || path == panel ||
		(strings.HasPrefix(path, panel+"/") && !strings.HasPrefix(path, panel+"/api/"))
}

// Retry only read-only panel module requests on transport failures or temporary
// upstream gateway errors. Do not replay API calls, mutations, 404s or auth errors.
// Responses remain streamed and retain the official compression/security headers.
type panelAssetTransport struct {
	base   http.RoundTripper
	prefix string
}

func (t *panelAssetTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	asset := (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
		strings.HasPrefix(r.URL.Path, t.prefix) &&
		(strings.HasSuffix(r.URL.Path, ".js") || strings.HasSuffix(r.URL.Path, ".css"))
	for attempt := 0; ; attempt++ {
		resp, err := t.base.RoundTrip(r)
		temporary := err != nil || (resp != nil && (resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504))
		if !asset || !temporary || attempt == 2 || r.Context().Err() != nil {
			return resp, err
		}
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 150 * time.Millisecond)
		select {
		case <-r.Context().Done():
			timer.Stop()
			return nil, r.Context().Err()
		case <-timer.C:
		}
	}
}
