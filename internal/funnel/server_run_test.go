package funnel

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func getFreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

func TestWithRecovery_PanicReturns500JSON(t *testing.T) {
	h := withRecovery(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://x/", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d want 500", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
		t.Errorf("content-type should be json: %q", rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(rec.Body.String(), "internal server error") {
		t.Errorf("body should be error json: %q", rec.Body.String())
	}
}

func TestWithRecovery_PassesThrough(t *testing.T) {
	h := withRecovery(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://x/", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Errorf("expected ok passthrough, got %d %q", rec.Code, rec.Body.String())
	}
}

func TestFlagsFromCmd_InvalidTailscale(t *testing.T) {
	cmd := newTestCmd(t, map[string]string{
		"route":     "app.example.com=http://127.0.0.1:8080",
		"auth-key":  "tskey-test",
		"tailscale": "bogus",
	})
	if _, err := FlagsFromCmd(cmd); err == nil {
		t.Fatal("expected error for invalid --tailscale")
	}
}

func TestFlagsFromCmd_EmptyHostInYAML(t *testing.T) {
	cfgPath := writeConfig(t, `
routes:
  - host: ""
    upstream: http://127.0.0.1:8080
`)
	cmd := newTestCmd(t, map[string]string{
		"config":   cfgPath,
		"auth-key": "tskey-test",
	})
	if _, err := FlagsFromCmd(cmd); err == nil {
		t.Fatal("expected error for empty host in YAML routes")
	}
}

func TestFlagsFromCmd_LocalModeNoAuthRequired(t *testing.T) {
	cmd := newTestCmd(t, map[string]string{
		"route":     "app.example.com=http://127.0.0.1:8080",
		"tailscale": "",
	})
	o, err := FlagsFromCmd(cmd)
	if err != nil {
		t.Fatalf("local mode must not require auth key: %v", err)
	}
	if o.Tailscale != "" {
		t.Errorf("tailscale should be empty (local): got %q", o.Tailscale)
	}
	if o.StateDir != "./.aria-tailscale-state" {
		t.Errorf("default state dir not applied: got %q", o.StateDir)
	}
	if o.Hostname != "ariacompute-funnel" {
		t.Errorf("default hostname not applied: got %q", o.Hostname)
	}
}

func TestFlagsFromCmd_AuthKeyEnvFallback(t *testing.T) {
	// Explicit empty --auth-key with env set must fall back to env.
	cmd := newTestCmd(t, map[string]string{
		"route":    "app.example.com=http://127.0.0.1:8080",
		"auth-key": "",
	})
	t.Setenv("TS_AUTHKEY", "tskey-env")
	o, err := FlagsFromCmd(cmd)
	if err != nil {
		t.Fatalf("env fallback should satisfy auth: %v", err)
	}
	if o.AuthKey != "tskey-env" {
		t.Errorf("expected env auth key, got %q", o.AuthKey)
	}
}

func TestFlagsFromCmd_DefaultsFromConfig(t *testing.T) {
	cfgPath := writeConfig(t, `
tailscale: funnel
listen: ":8081"
hostname: cfg-host
state_dir: /var/lib/cfg-state
routes:
  - host: app.example.com
    upstream: http://127.0.0.1:8080
`)
	cmd := newTestCmd(t, map[string]string{
		"config":   cfgPath,
		"auth-key": "tskey-test",
	})
	o, err := FlagsFromCmd(cmd)
	if err != nil {
		t.Fatalf("FlagsFromCmd: %v", err)
	}
	if o.Listen != ":8081" || o.Hostname != "cfg-host" || o.StateDir != "/var/lib/cfg-state" {
		t.Errorf("config defaults not applied: %+v", o)
	}
}

func TestFlagsFromCmd_ConfigExplicitEmptyTailscaleIsLocal(t *testing.T) {
	cfgPath := writeConfig(t, `
tailscale: ""
listen: ":8082"
routes:
  - host: app.example.com
    upstream: http://127.0.0.1:8080
`)
	cmd := newTestCmd(t, map[string]string{
		"config": cfgPath,
	})
	o, err := FlagsFromCmd(cmd)
	if err != nil {
		t.Fatalf("explicit empty tailscale in config must not require auth key: %v", err)
	}
	if o.Tailscale != "" {
		t.Errorf("tailscale should be empty (local) from explicit config: got %q", o.Tailscale)
	}
}

func TestRun_LocalMode_ServesAndShutsDown(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "upstream")
	}))
	defer up.Close()
	upHost := strings.TrimPrefix(up.URL, "http://")

	port := getFreePort(t)
	o := Options{
		Listen:    fmt.Sprintf("127.0.0.1:%d", port),
		Tailscale: "",
		Routes:    []Route{{Host: "test.local", Upstream: "http://" + upHost}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- Run(ctx, o) }()

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	rec := pollGet(t, base+"/", "test.local")
	if rec.Code != http.StatusOK {
		t.Fatalf("local request status: got %d", rec.Code)
	}
	if rec.Body.String() != "upstream" {
		t.Errorf("unexpected body: %q", rec.Body.String())
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil && err != context.Canceled {
			t.Logf("Run returned (acceptable): %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not shut down after context cancel")
	}
}

// pollGet retries a GET until the server answers (covers startup race) or times out.
// The Host header is set so the proxy's Host-based routing matches the route.
func pollGet(t *testing.T, url, host string) *httptest.ResponseRecorder {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	client := &http.Client{}
	for time.Now().Before(deadline) {
		// Use a real client so the request is actually dispatched over TCP,
		// exercising the full proxy path including Host-based routing.
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Host = host
		resp, err := client.Do(req)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			rec := httptest.NewRecorder()
			rec.Code = resp.StatusCode
			rec.Body.Write(body)
			if resp.StatusCode == http.StatusOK {
				return rec
			}
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("server not reachable at %q: last err %v", url, lastErr)
	return nil
}
