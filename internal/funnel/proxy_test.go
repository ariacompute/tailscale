package funnel

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestNewProxy_Errors(t *testing.T) {
	cases := []struct {
		name   string
		routes []Route
	}{
		{"empty host", []Route{{Host: "", Upstream: "http://127.0.0.1:1"}}},
		{"duplicate host", []Route{
			{Host: "a.example.com", Upstream: "http://127.0.0.1:1"},
			{Host: "a.example.com", Upstream: "http://127.0.0.1:2"},
		}},
		{"invalid upstream", []Route{{Host: "a.example.com", Upstream: "://not-a-url"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := NewProxy(c.routes); err == nil {
				t.Fatalf("expected error for %s", c.name)
			}
		})
	}
}

func TestProxy_CaseInsensitiveHost(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.Host)
	}))
	defer up.Close()
	upHost := strings.TrimPrefix(up.URL, "http://")

	p, err := NewProxy([]Route{{Host: "App.Example.com", Upstream: "http://" + upHost}})
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://app.example.com/", nil)
	req.Host = "APP.example.com"
	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("case-insensitive host status: got %d", rec.Code)
	}
}

func TestProxy_ForwardsPathAndQuery(t *testing.T) {
	var gotPath, gotQuery string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		_, _ = io.WriteString(w, "upstream-ok")
	}))
	defer up.Close()
	upHost := strings.TrimPrefix(up.URL, "http://")

	p, err := NewProxy([]Route{{Host: "app.example.com", Upstream: "http://" + upHost}})
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://app.example.com/api/v1/ping?x=1&y=2", nil)
	req.Host = "app.example.com"
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d", rec.Code)
	}
	if gotPath != "/api/v1/ping" {
		t.Errorf("path not forwarded: got %q", gotPath)
	}
	if gotQuery != "x=1&y=2" {
		t.Errorf("query not forwarded: got %q", gotQuery)
	}
}

func TestProxy_BadGatewayOnUpstreamFailure(t *testing.T) {
	// Upstream that is unreachable: use a closed listener's address.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadAddr := dead.URL
	dead.Close() // server gone -> connections fail

	deadHost := strings.TrimPrefix(deadAddr, "http://")
	p, err := NewProxy([]Route{{Host: "app.example.com", Upstream: "http://" + deadHost}})
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://app.example.com/", nil)
	req.Host = "app.example.com"
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 on upstream failure, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "bad gateway") {
		t.Errorf("body should be 'bad gateway', got %q", rec.Body.String())
	}
}

func TestProxy_XForwardedHostLowercased(t *testing.T) {
	var xfh string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		xfh = r.Header.Get("X-Forwarded-Host")
	}))
	defer up.Close()
	upHost := strings.TrimPrefix(up.URL, "http://")

	p, err := NewProxy([]Route{{Host: "app.example.com", Upstream: "http://" + upHost}})
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://APP.Example.com/", nil)
	req.Host = "APP.Example.com"
	p.ServeHTTP(rec, req)

	if xfh != "app.example.com" {
		t.Errorf("X-Forwarded-Host should be lowercased: got %q", xfh)
	}
}

func TestProxy_MultipleRoutes(t *testing.T) {
	makeUp := func(mark string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, mark)
		}))
	}
	pin := makeUp("pin")
	defer pin.Close()
	agent := makeUp("agent")
	defer agent.Close()

	p, err := NewProxy([]Route{
		{Host: "app.example.com", Upstream: pin.URL},
		{Host: "api.example.com", Upstream: agent.URL},
	})
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	for host, want := range map[string]string{
		"app.example.com": "pin",
		"api.example.com": "agent",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "http://"+host+"/", nil)
		req.Host = host
		p.ServeHTTP(rec, req)
		if rec.Body.String() != want {
			t.Errorf("host %q routed to %q, want %q", host, rec.Body.String(), want)
		}
	}
}

// ensure url import is exercised (Parse of upstream happens inside NewProxy).
var _ = url.Parse
