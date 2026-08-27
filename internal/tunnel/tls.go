package tunnel

import (
	"context"
	"crypto/tls"
	"net"
)

type tlsDialer struct {
	addr string
	cfg  *tls.Config
}

func NewTLSDialer(addr string, cfg *tls.Config) Dialer { return &tlsDialer{addr, cfg} }

func (d *tlsDialer) Dial(ctx context.Context) (net.Conn, error) {
	return (&tls.Dialer{Config: d.cfg}).DialContext(ctx, "tcp", d.addr)
}

type tlsListener struct{ net.Listener }

func NewTLSListener(addr string, cfg *tls.Config) (Listener, error) {
	ln, err := tls.Listen("tcp", addr, cfg)
	if err != nil {
		return nil, err
	}
	return &tlsListener{ln}, nil
}
