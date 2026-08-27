// Package client is proxyc: it keeps one outbound tunnel to the server, registers
// its services, and splices each server-opened stream to the named local service.
package client

import (
	"context"
	"log/slog"
	"net"
	"time"

	"github.com/mannk98/gotun/internal/proto"
	"github.com/mannk98/gotun/internal/tunnel"
)

type Config struct {
	Dialer   tunnel.Dialer
	Token    string
	ClientID string
	Services []proto.Service
}

type Client struct {
	cfg    Config
	byName map[string]string
}

func New(cfg Config) *Client {
	byName := map[string]string{}
	for _, s := range cfg.Services {
		byName[s.Name] = s.Local
	}
	return &Client{cfg: cfg, byName: byName}
}

func (c *Client) Run(ctx context.Context) error {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := c.connectOnce(ctx)
		slog.Warn("tunnel disconnected", "err", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (c *Client) connectOnce(ctx context.Context) error {
	conn, err := c.cfg.Dialer.Dial(ctx)
	if err != nil {
		return err
	}
	sess, err := tunnel.ClientSession(conn)
	if err != nil {
		conn.Close()
		return err
	}
	defer sess.Close()

	ctrl, err := sess.OpenStream()
	if err != nil {
		return err
	}
	if err := proto.WriteHello(ctrl, proto.Hello{
		Token: c.cfg.Token, ClientID: c.cfg.ClientID, Services: c.cfg.Services,
	}); err != nil {
		return err
	}
	ack, err := proto.ReadAck(ctrl)
	if err != nil {
		return err
	}
	if !ack.OK {
		return &authError{ack.Error}
	}
	slog.Info("tunnel up", "endpoints", ack.Endpoints)

	for {
		st, err := sess.AcceptStream()
		if err != nil {
			return err
		}
		go c.handleStream(st)
	}
}

func (c *Client) handleStream(st net.Conn) {
	service, err := proto.ReadStreamHeader(st)
	if err != nil {
		st.Close()
		return
	}
	local, ok := c.byName[service]
	if !ok {
		slog.Warn("unknown service", "service", service)
		st.Close()
		return
	}
	lc, err := net.Dial("tcp", local)
	if err != nil {
		slog.Warn("dial local failed", "service", service, "addr", local, "err", err)
		st.Close()
		return
	}
	tunnel.Pipe(st, lc)
}

type authError struct{ msg string }

func (e *authError) Error() string { return "server rejected auth: " + e.msg }
