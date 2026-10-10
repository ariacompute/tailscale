package funnel

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// Proxy routes incoming requests to an internal upstream based on the request
// Host header. Unknown hosts return 404 so the Funnel node never forwards
// arbitrary traffic into the internal network.
type Proxy struct {
	routes map[string]*httputil.ReverseProxy
}

// NewProxy builds a reverse proxy per host. It fails if any route is invalid.
func NewProxy(routes []Route) (*Proxy, error) {
	m := make(map[string]*httputil.ReverseProxy, len(routes))
	order := make([]string, 0, len(routes))
	for _, r := range routes {
		key := strings.ToLower(strings.TrimSpace(r.Host))
		if key == "" {
			return nil, fmt.Errorf("route host must not be empty")
		}
		if _, dup := m[key]; dup {
			return nil, fmt.Errorf("duplicate route host %q", r.Host)
		}
		target, err := url.Parse(r.Upstream)
		if err != nil {
			return nil, fmt.Errorf("route %q upstream %q invalid: %w", r.Host, r.Upstream, err)
		}
		base := *target
		rp := httputil.NewSingleHostReverseProxy(&base)
		rp.ErrorHandler = func(w http.ResponseWriter, req *http.Request, perr error) {
			http.Error(w, "bad gateway", http.StatusBadGateway)
			// perr intentionally unlogged here; Funnel logs upstream failures.
			_ = perr
		}
		m[key] = rp
		order = append(order, key)
	}
	return &Proxy{routes: m}, nil
}

// ServeHTTP dispatches by Host (port stripped, lower-cased). Unknown hosts 404.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	host = strings.ToLower(strings.TrimSpace(host))

	rp, ok := p.routes[host]
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	// httputil sets X-Forwarded-For automatically; record the original Host too.
	r.Header.Set("X-Forwarded-Host", host)
	rp.ServeHTTP(w, r)
}
