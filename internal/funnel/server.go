package funnel

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"tailscale.com/tsnet"
)

// Options is the fully-resolved runtime configuration.
type Options struct {
	ConfigFile string
	Listen     string
	Tailscale  string // "", "funnel", "tailnet"
	Hostname   string
	StateDir   string
	AuthKey    string
	Routes     []Route
}

// RegisterFlags registers all CLI flags on the given command.
func RegisterFlags(cmd *cobra.Command) {
	cmd.Flags().String("config", "", "Path to YAML config file (routes + settings)")
	cmd.Flags().String("listen", ":8787", "Listen address when not using Tailscale, or tailnet/funnel bind (e.g. 127.0.0.1:8787 or :443)")
	cmd.Flags().String("tailscale", "funnel", `Tailscale integration: "funnel" (public HTTPS via Funnel), "tailnet" (private mesh HTTP only), or empty for local only`)
	cmd.Flags().String("hostname", "", "Tailscale hostname for this node (tsnet) (default ariacompute-funnel)")
	cmd.Flags().String("state-dir", "", "Tailscale state directory (tsnet) (default ./.aria-tailscale-state)")
	cmd.Flags().String("auth-key", "", "Tailscale auth key (default TS_AUTHKEY env, then TS_AUTHKEY_FILE)")
	cmd.Flags().StringArray("route", nil, "Route host=upstream (repeatable; overrides YAML routes with the same host)")
}

// FlagsFromCmd resolves Options from CLI flags, environment, and an optional
// YAML config file. Precedence (lowest to highest):
//
//	built-in defaults -> YAML file -> CLI flags -> TS_AUTHKEY env ->
//	TS_AUTHKEY_FILE (file) -> error
func FlagsFromCmd(cmd *cobra.Command) (Options, error) {
	var o Options

	o.ConfigFile, _ = cmd.Flags().GetString("config")

	cfg := defaultConfig()
	if o.ConfigFile != "" {
		loaded, err := LoadConfig(o.ConfigFile)
		if err != nil {
			return o, fmt.Errorf("load config %q: %w", o.ConfigFile, err)
		}
		applyConfig(&cfg, loaded)
	}

	setStr := func(flag, cur string) string {
		if cmd.Flags().Changed(flag) {
			if v, err := cmd.Flags().GetString(flag); err == nil && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
		return cur
	}

	o.Listen = setStr("listen", cfg.Listen)
	// tailscale: an explicitly-set (even empty) value wins, because "" means
	// local mode and must not fall back to the "funnel" default.
	if cmd.Flags().Changed("tailscale") {
		if v, err := cmd.Flags().GetString("tailscale"); err == nil {
			o.Tailscale = strings.TrimSpace(v)
		} else {
			o.Tailscale = strings.TrimSpace(cfg.Tailscale)
		}
	} else {
		o.Tailscale = strings.TrimSpace(cfg.Tailscale)
	}
	o.Hostname = setStr("hostname", cfg.Hostname)
	o.StateDir = setStr("state-dir", cfg.StateDir)
	o.AuthKey = setStr("auth-key", cfg.AuthKey)

	// Routes: YAML first, then CLI --route overrides per host.
	routes := make(map[string]Route, len(cfg.Routes))
	for _, r := range cfg.Routes {
		key := strings.ToLower(strings.TrimSpace(r.Host))
		if key == "" {
			return o, fmt.Errorf("config route with empty host")
		}
		routes[key] = r
	}
	cliRoutes, _ := cmd.Flags().GetStringArray("route")
	for _, s := range cliRoutes {
		r, err := ParseRoute(s)
		if err != nil {
			return o, err
		}
		routes[strings.ToLower(strings.TrimSpace(r.Host))] = r
	}
	if len(routes) == 0 {
		return o, errors.New("no routes configured: provide --route host=upstream or a config file with routes")
	}
	o.Routes = make([]Route, 0, len(routes))
	for _, r := range routes {
		o.Routes = append(o.Routes, r)
	}

	// Auth key: only required when actually joining a tailnet (funnel/tailnet).
	o.Tailscale = strings.TrimSpace(strings.ToLower(o.Tailscale))
	switch o.Tailscale {
	case "", "funnel", "tailnet":
	default:
		return o, fmt.Errorf(`--tailscale must be "", "funnel", or "tailnet"`)
	}
	if o.StateDir == "" {
		o.StateDir = "./.aria-tailscale-state"
	}
	if o.Hostname == "" {
		o.Hostname = "ariacompute-funnel"
	}

	// Auth key: required only when joining a tailnet (funnel/tailnet). The
	// secret is resolved from --auth-key / YAML auth_key, then the TS_AUTHKEY
	// env, then a TS_AUTHKEY_FILE (a 0600 file) so it never sits on the
	// command line. The package never ships a key.
	if o.Tailscale != "" {
		o.AuthKey = strings.TrimSpace(o.AuthKey)
		if o.AuthKey == "" {
			o.AuthKey = strings.TrimSpace(os.Getenv("TS_AUTHKEY"))
		}
		if o.AuthKey == "" {
			if p := strings.TrimSpace(os.Getenv("TS_AUTHKEY_FILE")); p != "" {
				b, err := os.ReadFile(p)
				if err != nil {
					return o, fmt.Errorf("read TS_AUTHKEY_FILE %q: %w", p, err)
				}
				o.AuthKey = strings.TrimSpace(string(b))
			}
		}
		if o.AuthKey == "" {
			return o, errors.New("tailscale auth key required: set --auth-key, TS_AUTHKEY, or TS_AUTHKEY_FILE (Funnel/tailnet need a reusable auth key)")
		}
	}
	return o, nil
}

// Run starts the proxy until ctx is cancelled.
func Run(ctx context.Context, o Options) error {
	proxy, err := NewProxy(o.Routes)
	if err != nil {
		return fmt.Errorf("build proxy: %w", err)
	}
	log.Printf("aria-tailscale: %d route(s) configured", len(o.Routes))
	for _, r := range o.Routes {
		log.Printf("  %s -> %s", r.Host, r.Upstream)
	}

	handler := withRecovery(proxy)

	switch o.Tailscale {
	case "":
		return runLocal(ctx, o.Listen, handler)
	case "tailnet":
		return runTSNet(ctx, o, handler, false)
	case "funnel":
		return runTSNet(ctx, o, handler, true)
	default:
		return errors.New("invalid tailscale mode")
	}
}

func runLocal(ctx context.Context, listen string, h http.Handler) error {
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("listen %q: %w (is another process using this port?)", listen, err)
	}
	srv := &http.Server{Handler: h}
	errCh := make(chan error, 1)
	go func() {
		log.Printf("aria-tailscale listening on http://%s (local)", ln.Addr().String())
		errCh <- srv.Serve(ln)
	}()
	select {
	case <-ctx.Done():
		_ = srv.Shutdown(context.Background())
		return ctx.Err()
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	}
}

func runTSNet(ctx context.Context, o Options, h http.Handler, funnel bool) error {
	srv := &tsnet.Server{
		Dir:      o.StateDir,
		Hostname: o.Hostname,
		AuthKey:  o.AuthKey,
		Logf:     log.Printf,
		UserLogf: log.Printf,
	}
	defer srv.Close()

	addr := o.Listen
	remapped8787To443 := false
	if funnel {
		if !strings.Contains(addr, ":") {
			addr = ":" + addr
		}
		if addr == ":8787" {
			remapped8787To443 = true
			addr = ":443"
		} else if addr == ":0" {
			addr = ":443"
		}
	}

	var ln net.Listener
	var err error
	if funnel {
		ln, err = srv.ListenFunnel("tcp", addr)
	} else {
		ln, err = srv.Listen("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("tailscale listen: %w", err)
	}
	defer ln.Close()

	https := funnel
	logURL(srv, o.Hostname, https)

	httpSrv := &http.Server{Handler: h}
	errCh := make(chan error, 1)
	go func() {
		errCh <- httpSrv.Serve(ln)
	}()

	var localSrv *http.Server
	if remapped8787To443 {
		lln, err := net.Listen("tcp", ":8787")
		if err != nil {
			log.Printf("warning: could not bind :8787 for local HTTP (Funnel listens on %s): %v", addr, err)
		} else {
			localSrv = &http.Server{Handler: h}
			go func() {
				log.Print("aria-tailscale also listening on http://127.0.0.1:8787 and http://[::1]:8787 (local; same routes as Funnel)")
				if err := localSrv.Serve(lln); err != nil && err != http.ErrServerClosed {
					log.Printf("local :8787 listener: %v", err)
				}
			}()
		}
	}

	shutdown := func() {
		shctx := context.Background()
		_ = httpSrv.Shutdown(shctx)
		if localSrv != nil {
			_ = localSrv.Shutdown(shctx)
		}
	}

	select {
	case <-ctx.Done():
		shutdown()
		return ctx.Err()
	case err := <-errCh:
		shutdown()
		if err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	}
}

func logURL(s *tsnet.Server, hostname string, https bool) {
	scheme := "http"
	if https {
		scheme = "https"
	}
	doms := s.CertDomains()
	if len(doms) > 0 {
		log.Printf("aria-tailscale on Tailscale: %s://%s", scheme, doms[0])
		return
	}
	log.Printf("aria-tailscale listening for hostname %q (check `tailscale status` for MagicDNS name)", hostname)
}

// withRecovery guards the proxy against panics, returning JSON on panic.
func withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("aria-tailscale panic: %v", err)
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"internal server error"}`))
			}
		}()
		next.ServeHTTP(w, r)
	})
}
