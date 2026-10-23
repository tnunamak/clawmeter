package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/cliproxy"
	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider/all"
)

const hubKeyEnv = "CLAWMETER_HUB_KEY"

// hubCmd serves the configured Claude and Codex sources to T3 Code as a
// read-only CLIProxyAPI hub. It answers from the cache the tray keeps.
func hubCmd(args []string) int {
	fs := flag.NewFlagSet("hub", flag.ExitOnError)
	listen := fs.String("listen", "127.0.0.1:8318", "HTTP listen address (plain HTTP is limited to loopback)")
	tlsCert := fs.String("tls-cert", "", "TLS certificate file for a remote listener")
	tlsKey := fs.String("tls-key", "", "TLS private key file for a remote listener")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "clawmeter: hub does not take positional arguments")
		return 1
	}

	managementKey := os.Getenv(hubKeyEnv)
	if len(managementKey) < 32 || strings.TrimSpace(managementKey) != managementKey {
		fmt.Fprintf(os.Stderr, "clawmeter: set %s to a random secret of at least 32 characters with no surrounding whitespace\n", hubKeyEnv)
		return 1
	}
	if (*tlsCert == "") != (*tlsKey == "") {
		fmt.Fprintln(os.Stderr, "clawmeter: --tls-cert and --tls-key must be set together")
		return 1
	}
	address := listenAddress(*listen)
	if !isLoopbackAddress(address) && *tlsCert == "" {
		fmt.Fprintln(os.Stderr, "clawmeter: refusing plain HTTP on a non-loopback address; configure TLS or use a loopback reverse proxy")
		return 1
	}

	cfg, err := config.Load(all.SourceValidator())
	if err != nil {
		fmt.Fprintln(os.Stderr, "clawmeter:", err)
		return 1
	}
	registry := newRegistry(cfg)
	// A reading stays current for two tray polls, so one late poll does not
	// blank the hub.
	maxAge := config.ReadingMaxAge(cfg.Settings.PollInterval)
	handler, err := cliproxy.NewHandler(managementKey, registry.GetConfigured(), maxAge)
	if err != nil {
		fmt.Fprintln(os.Stderr, "clawmeter:", err)
		return 1
	}

	server := &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	scheme := "http"
	if *tlsCert != "" {
		scheme = "https"
	}
	fmt.Fprintf(os.Stderr, "clawmeter: read-only CLIProxyAPI-compatible hub listening on %s://%s\n", scheme, address)

	if *tlsCert != "" {
		err = server.ListenAndServeTLS(*tlsCert, *tlsKey)
	} else {
		err = server.ListenAndServe()
	}
	if err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "clawmeter: hub server: %v\n", err)
		return 1
	}
	return 0
}

// listenAddress spells localhost as 127.0.0.1, so the address checked is
// the address served and no resolver can move it off this computer.
func listenAddress(address string) string {
	if host, port, err := net.SplitHostPort(address); err == nil && strings.EqualFold(host, "localhost") {
		return net.JoinHostPort("127.0.0.1", port)
	}
	return address
}

// isLoopbackAddress reports whether address is a loopback IP and port.
func isLoopbackAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
