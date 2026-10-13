package funnel

import (
	"path/filepath"
	"testing"
)

func TestLoadConfig_Valid(t *testing.T) {
	cfgPath := writeConfig(t, `
tailscale: tailnet
listen: ":9000"
hostname: my-host
state_dir: /tmp/state
auth_key: tskey-x
routes:
  - host: app.example.com
    upstream: http://127.0.0.1:8080
`)
	c, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if c.Tailscale != "tailnet" || c.Listen != ":9000" || c.Hostname != "my-host" {
		t.Errorf("scalar mismatch: %+v", c)
	}
	if c.StateDir != "/tmp/state" || c.AuthKey != "tskey-x" {
		t.Errorf("state/auth mismatch: %+v", c)
	}
	if len(c.Routes) != 1 || c.Routes[0].Upstream != "http://127.0.0.1:8080" {
		t.Errorf("routes mismatch: %+v", c.Routes)
	}
	if c.ConfigFile != cfgPath {
		t.Errorf("ConfigFile not set: %q", c.ConfigFile)
	}
}

func TestLoadConfig_MissingFile(t *testing.T) {
	if _, err := LoadConfig(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	p := writeConfig(t, "tailscale: [unbalanced\nroutes: oops")
	if _, err := LoadConfig(p); err == nil {
		t.Fatal("expected YAML parse error")
	}
}

func TestDefaultConfig(t *testing.T) {
	c := defaultConfig()
	if c.Tailscale != "funnel" || c.Listen != ":8787" {
		t.Errorf("default mismatch: %+v", c)
	}
	if c.Hostname != "ariacompute-funnel" || c.StateDir != "./.aria-tailscale-state" {
		t.Errorf("default mismatch: %+v", c)
	}
}

func TestApplyConfig_OverridesOnlyNonEmpty(t *testing.T) {
	dst := defaultConfig()
	src := &Config{
		Tailscale: "", // absent/implicit empty -> must NOT override
		Listen:    ":1234",
		Hostname:  "src-host",
		StateDir:  "",
		AuthKey:   "tskey-src",
		Routes:    []Route{{Host: "a.example.com", Upstream: "http://127.0.0.1:1"}},
	}
	applyConfig(&dst, src)

	if dst.Tailscale != "funnel" {
		t.Errorf("implicit empty src.Tailscale should not override: got %q", dst.Tailscale)
	}
	if dst.Listen != ":1234" {
		t.Errorf("non-empty listen should override: got %q", dst.Listen)
	}
	if dst.StateDir != "./.aria-tailscale-state" {
		t.Errorf("empty src.StateDir should not override: got %q", dst.StateDir)
	}
	if dst.AuthKey != "tskey-src" {
		t.Errorf("auth_key should override: got %q", dst.AuthKey)
	}
	if len(dst.Routes) != 1 {
		t.Errorf("routes should be appended: got %d", len(dst.Routes))
	}
}

func TestApplyConfig_ExplicitEmptyTailscaleIsLocal(t *testing.T) {
	dst := defaultConfig()
	src := &Config{
		Tailscale:         "", // explicit empty -> local
		tailscaleExplicit: true,
		Listen:            ":1234",
	}
	applyConfig(&dst, src)

	if dst.Tailscale != "" {
		t.Errorf("explicit empty src.Tailscale should set local (empty): got %q", dst.Tailscale)
	}
	if dst.Listen != ":1234" {
		t.Errorf("non-empty listen should override: got %q", dst.Listen)
	}
}

func TestParseRoute_EdgeCases(t *testing.T) {
	cases := []struct {
		in      string
		wantErr bool
		host    string
		up      string
	}{
		{in: "  App.Example.com = http://10.0.0.1:80  ", host: "App.Example.com", up: "http://10.0.0.1:80"},
		{in: "host=http://h/with=equals", host: "host", up: "http://h/with=equals"}, // '=' in upstream kept
		{in: "host=", wantErr: true},
		{in: "=upstream", wantErr: true},
		{in: "   ", wantErr: true},
		{in: "novalue", wantErr: true},
	}
	for _, c := range cases {
		r, err := ParseRoute(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseRoute(%q): expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseRoute(%q): unexpected error %v", c.in, err)
			continue
		}
		if r.Host != c.host || r.Upstream != c.up {
			t.Errorf("ParseRoute(%q) = %+v, want host=%q up=%q", c.in, r, c.host, c.up)
		}
	}
}

func TestParseRoute_PreservesRawHostCase(t *testing.T) {
	r, err := ParseRoute("App.Example.com=http://127.0.0.1:9")
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	if r.Host != "App.Example.com" {
		t.Errorf("host must keep original case: got %q", r.Host)
	}
	if r.Upstream != "http://127.0.0.1:9" {
		t.Errorf("upstream mismatch: got %q", r.Upstream)
	}
}
