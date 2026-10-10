package funnel

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// newTestCmd builds a cobra command with funnel flags set programmatically.
func newTestCmd(t *testing.T, sets map[string]string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "test"}
	RegisterFlags(cmd)
	for k, v := range sets {
		if err := cmd.Flags().Set(k, v); err != nil {
			t.Fatalf("set flag %q=%q: %v", k, v, err)
		}
	}
	return cmd
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return p
}

func TestFlagsFromCmd_RoutePriority(t *testing.T) {
	cfgPath := writeConfig(t, `
tailscale: tailnet
listen: ":9000"
hostname: yaml-host
routes:
  - host: app.example.com
    upstream: http://127.0.0.1:8080
  - host: api.example.com
    upstream: http://127.0.0.1:9090
`)

	cmd := newTestCmd(t, map[string]string{
		"config":   cfgPath,
		"auth-key": "tskey-test",
		"route":    "app.example.com=http://127.0.0.1:8081",
	})

	o, err := FlagsFromCmd(cmd)
	if err != nil {
		t.Fatalf("FlagsFromCmd: %v", err)
	}

	if o.Tailscale != "tailnet" {
		t.Errorf("tailscale from YAML: got %q want tailnet", o.Tailscale)
	}
	if o.Listen != ":9000" {
		t.Errorf("listen from YAML: got %q want :9000", o.Listen)
	}
	if o.Hostname != "yaml-host" {
		t.Errorf("hostname from YAML: got %q want yaml-host", o.Hostname)
	}

	byHost := map[string]string{}
	for _, r := range o.Routes {
		byHost[strings.ToLower(r.Host)] = r.Upstream
	}
	if byHost["app.example.com"] != "http://127.0.0.1:8081" {
		t.Errorf("CLI --route must override YAML pin: got %q", byHost["app.example.com"])
	}
	if byHost["api.example.com"] != "http://127.0.0.1:9090" {
		t.Errorf("YAML agent route must survive: got %q", byHost["api.example.com"])
	}
}

func TestFlagsFromCmd_CLIFlagOverridesYAML(t *testing.T) {
	cfgPath := writeConfig(t, `
tailscale: tailnet
routes:
  - host: app.example.com
    upstream: http://127.0.0.1:8080
`)
	cmd := newTestCmd(t, map[string]string{
		"config":    cfgPath,
		"auth-key":  "tskey-test",
		"tailscale": "funnel",
	})
	o, err := FlagsFromCmd(cmd)
	if err != nil {
		t.Fatalf("FlagsFromCmd: %v", err)
	}
	if o.Tailscale != "funnel" {
		t.Errorf("CLI --tailscale must override YAML: got %q want funnel", o.Tailscale)
	}
}

func TestFlagsFromCmd_RequiresAuthKey(t *testing.T) {
	cmd := newTestCmd(t, map[string]string{
		"route": "app.example.com=http://127.0.0.1:8080",
	})
	if _, err := FlagsFromCmd(cmd); err == nil {
		t.Fatal("expected error when auth key missing")
	}

	cmd2 := newTestCmd(t, map[string]string{
		"route": "app.example.com=http://127.0.0.1:8080",
	})
	cmd2.Flags().Set("auth-key", "")
	t.Setenv("TS_AUTHKEY", "tskey-env")
	if _, err := FlagsFromCmd(cmd2); err != nil {
		t.Fatalf("TS_AUTHKEY env should satisfy auth key: %v", err)
	}
}

func TestFlagsFromCmd_RequiresRoutes(t *testing.T) {
	cmd := newTestCmd(t, map[string]string{"auth-key": "tskey-test"})
	if _, err := FlagsFromCmd(cmd); err == nil {
		t.Fatal("expected error when no routes configured")
	}
}

func TestProxy_RoutingAnd404(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		xhost := r.Header.Get("X-Forwarded-Host")
		_, _ = io.WriteString(w, "ok:"+xhost)
	}))
	defer upstream.Close()

	upHost := strings.TrimPrefix(upstream.URL, "http://")
	routes := []Route{
		{Host: "app.example.com", Upstream: "http://" + upHost},
	}
	p, err := NewProxy(routes)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}

	// Known host -> proxied.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://app.example.com/foo", nil)
	req.Host = "app.example.com"
	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("known host status: got %d want 200", rec.Code)
	}
	if !strings.HasPrefix(rec.Body.String(), "ok:app.example.com") {
		t.Errorf("X-Forwarded-Host not propagated: body=%q", rec.Body.String())
	}

	// Host with port stripped.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "http://app.example.com:443/foo", nil)
	req2.Host = "app.example.com:443"
	p.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("host:port status: got %d want 200", rec2.Code)
	}

	// Unknown host -> 404.
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodGet, "http://evil.example.com/", nil)
	req3.Host = "evil.example.com"
	p.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusNotFound {
		t.Fatalf("unknown host status: got %d want 404", rec3.Code)
	}
}

func TestParseRoute(t *testing.T) {
	r, err := ParseRoute("app.example.com=http://127.0.0.1:8080")
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	if r.Host != "app.example.com" || r.Upstream != "http://127.0.0.1:8080" {
		t.Errorf("parse mismatch: %+v", r)
	}
	if _, err := ParseRoute("noequals"); err == nil {
		t.Error("expected error for missing '='")
	}
	if _, err := ParseRoute("=http://x"); err == nil {
		t.Error("expected error for empty host")
	}
}
