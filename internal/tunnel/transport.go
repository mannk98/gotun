// Package tunnel is the transport-agnostic core: a Dialer/Listener pair that
// yields net.Conn (raw-TLS or WSS), plus yamux session helpers and a byte pipe.
package tunnel

import (
	"context"
	"net"
)

// Dialer opens one client->server tunnel connection.
type Dialer interface {
	Dial(ctx context.Context) (net.Conn, error)
}

// Listener accepts server-side tunnel connections.
type Listener interface {
	Accept() (net.Conn, error)
	Addr() net.Addr
	Close() error
}
