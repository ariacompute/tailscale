package funnel

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Route maps an external Host (domain) to an internal upstream URL.
type Route struct {
	Host     string `yaml:"host" json:"host"`
	Upstream string `yaml:"upstream" json:"upstream"`
}

// Config is the YAML configuration shape. ConfigFile is populated from the
// --config flag and is never read from the file itself.
type Config struct {
	ConfigFile string  `yaml:"-"`
	Tailscale  string  `yaml:"tailscale"`
	Listen     string  `yaml:"listen"`
	Hostname   string  `yaml:"hostname"`
	StateDir   string  `yaml:"state_dir"`
	AuthKey    string  `yaml:"auth_key"`
	Routes     []Route `yaml:"routes"`
	// tailscaleExplicit records whether the YAML file set the tailscale key
	// (even to an empty string). An explicit empty value means local mode;
	// without this flag an empty value is indistinguishable from the key being
	// absent and would fall back to the "funnel" default.
	tailscaleExplicit bool
}

// defaultConfig returns the built-in defaults used before any YAML or CLI input.
func defaultConfig() Config {
	return Config{
		Tailscale: "funnel",
		Listen:    ":8787",
		Hostname:  "ariacompute-funnel",
		StateDir:  "./.aria-tailscale-state",
	}
}

// LoadConfig reads and parses a YAML configuration file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	// Detect whether the tailscale key was explicitly present in the file so an
	// explicit empty value ("") can mean local mode instead of falling back to
	// the "funnel" default.
	var m map[string]interface{}
	if err := yaml.Unmarshal(data, &m); err == nil {
		if _, ok := m["tailscale"]; ok {
			c.tailscaleExplicit = true
		}
	}
	c.ConfigFile = path
	return &c, nil
}

// ParseRoute parses a "host=upstream" string into a Route.
func ParseRoute(s string) (Route, error) {
	var r Route
	s = strings.TrimSpace(s)
	if s == "" {
		return r, fmt.Errorf("empty --route value")
	}
	idx := strings.Index(s, "=")
	if idx < 0 {
		return r, fmt.Errorf("invalid --route %q: expected host=upstream", s)
	}
	host := strings.TrimSpace(s[:idx])
	upstream := strings.TrimSpace(s[idx+1:])
	if host == "" || upstream == "" {
		return r, fmt.Errorf("invalid --route %q: host and upstream must be non-empty", s)
	}
	r.Host = host
	r.Upstream = upstream
	return r, nil
}

// applyConfig overlays scalar fields and appends routes from src onto dst.
// An explicitly-set tailscale value (including empty, meaning local) wins; an
// absent key leaves the default/fallback untouched. Other scalars only override
// when non-empty.
func applyConfig(dst *Config, src *Config) {
	if src.tailscaleExplicit {
		dst.Tailscale = src.Tailscale
	}
	if strings.TrimSpace(src.Listen) != "" {
		dst.Listen = src.Listen
	}
	if strings.TrimSpace(src.Hostname) != "" {
		dst.Hostname = src.Hostname
	}
	if strings.TrimSpace(src.StateDir) != "" {
		dst.StateDir = src.StateDir
	}
	if strings.TrimSpace(src.AuthKey) != "" {
		dst.AuthKey = src.AuthKey
	}
	dst.Routes = append(dst.Routes, src.Routes...)
}
