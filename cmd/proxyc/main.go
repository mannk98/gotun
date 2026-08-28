// Command proxyc is the gotun client, run inside the NATed container.
// Env: GOTUN_SERVER(wss://host:443/tunnel) GOTUN_TOKEN
//
//	GOTUN_SERVICES(novnc=127.0.0.1:8080,ssh=127.0.0.1:22)
//	GOTUN_CLIENT_ID(optional; default random) GOTUN_INSECURE(1 = skip TLS verify)
package main

import (
	"context"
	"crypto/tls"
	"log/slog"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/mannk98/gotun/internal/client"
	"github.com/mannk98/gotun/internal/proto"
	"github.com/mannk98/gotun/internal/tunnel"
)

func parseServices(s string) []proto.Service {
	var out []proto.Service
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		name, addr, ok := strings.Cut(pair, "=")
		if !ok {
			slog.Warn("malformed GOTUN_SERVICES entry, skipping", "entry", pair)
			continue
		}
		name, addr = strings.TrimSpace(name), strings.TrimSpace(addr)
		if name == "" || addr == "" {
			slog.Warn("malformed GOTUN_SERVICES entry, skipping", "entry", pair)
			continue
		}
		out = append(out, proto.Service{Name: name, Local: addr})
	}
	return out
}

func main() {
	server := os.Getenv("GOTUN_SERVER")
	token := os.Getenv("GOTUN_TOKEN")
	services := parseServices(os.Getenv("GOTUN_SERVICES"))
	if server == "" || token == "" || len(services) == 0 {
		slog.Error("GOTUN_SERVER, GOTUN_TOKEN and GOTUN_SERVICES are required")
		os.Exit(1)
	}
	id := os.Getenv("GOTUN_CLIENT_ID")
	if id == "" {
		id = uuid.NewString()
	}
	tlsCfg := &tls.Config{}
	if os.Getenv("GOTUN_INSECURE") == "1" {
		tlsCfg.InsecureSkipVerify = true // testing only
	}
	dialer := tunnel.NewWSSDialer(server, tlsCfg, true) // proxyFromEnv=true -> HTTPS_PROXY
	c := client.New(client.Config{Dialer: dialer, Token: token, ClientID: id, Services: services})
	slog.Info("proxyc starting", "server", server, "client_id", id, "services", len(services))
	if err := c.Run(context.Background()); err != nil {
		slog.Error("run", "err", err)
		os.Exit(1)
	}
}
