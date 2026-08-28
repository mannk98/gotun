// Command proxyd is the gotun public-side server.
// Env: GOTUN_LISTEN(:443) GOTUN_PATH(/tunnel) GOTUN_TOKEN GOTUN_TLS_CERT GOTUN_TLS_KEY
//
//	GOTUN_PORT_MIN(20000) GOTUN_PORT_MAX(20999) GOTUN_PUBLIC_HOST(localhost)
package main

import (
	"context"
	"crypto/tls"
	"log/slog"
	"os"
	"strconv"

	"github.com/mannk98/gotun/internal/relay"
	"github.com/mannk98/gotun/internal/tunnel"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func atoi(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

func main() {
	token := os.Getenv("GOTUN_TOKEN")
	if token == "" {
		slog.Error("GOTUN_TOKEN is required")
		os.Exit(1)
	}
	cert, err := tls.LoadX509KeyPair(os.Getenv("GOTUN_TLS_CERT"), os.Getenv("GOTUN_TLS_KEY"))
	if err != nil {
		slog.Error("load TLS keypair", "err", err)
		os.Exit(1)
	}
	tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}}
	ln, err := tunnel.NewWSSListener(env("GOTUN_LISTEN", ":443"), env("GOTUN_PATH", "/tunnel"), tlsCfg)
	if err != nil {
		slog.Error("tunnel listen", "err", err)
		os.Exit(1)
	}
	portMin := atoi(os.Getenv("GOTUN_PORT_MIN"), 20000)
	portMax := atoi(os.Getenv("GOTUN_PORT_MAX"), 20999)
	if portMin <= 0 || portMax <= 0 || portMin > portMax {
		slog.Error("invalid port range", "port_min", portMin, "port_max", portMax)
		os.Exit(1)
	}
	srv := relay.NewServer(relay.Config{
		Token:      token,
		PublicHost: env("GOTUN_PUBLIC_HOST", "localhost"),
		PortMin:    portMin,
		PortMax:    portMax,
	}, ln)
	slog.Info("proxyd listening", "listen", env("GOTUN_LISTEN", ":443"))
	if err := srv.Serve(context.Background()); err != nil {
		slog.Error("serve", "err", err)
		os.Exit(1)
	}
}
