package tunnel

import (
	"io"
	"net"

	"github.com/hashicorp/yamux"
)

func cfg() *yamux.Config {
	c := yamux.DefaultConfig()
	c.LogOutput = io.Discard // route through our own slog at call sites, not yamux's logger
	return c
}

func ClientSession(conn net.Conn) (*yamux.Session, error) { return yamux.Client(conn, cfg()) }
func ServerSession(conn net.Conn) (*yamux.Session, error) { return yamux.Server(conn, cfg()) }

// Pipe copies bytes both ways between a and b until either direction ends,
// then closes both. Used to splice a public visitor to a local service.
func Pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		io.Copy(dst, src)
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)
	<-done
	a.Close()
	b.Close()
}
