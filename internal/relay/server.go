package relay

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/mannk98/gotun/internal/proto"
	"github.com/mannk98/gotun/internal/tunnel"
)

type Config struct {
	Token      string
	PublicHost string // host advertised back in the ack (e.g. the server's public DNS)
	PortMin    int
	PortMax    int
}

type Server struct {
	cfg Config
	ln  tunnel.Listener
	reg *Registry
}

func NewServer(cfg Config, ln tunnel.Listener) *Server {
	return &Server{cfg: cfg, ln: ln, reg: NewRegistry()}
}

func (s *Server) Serve(ctx context.Context) error {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return err
		}
		go s.handle(ctx, conn)
	}
}

func (s *Server) handle(ctx context.Context, conn net.Conn) {
	sess, err := tunnel.ServerSession(conn)
	if err != nil {
		conn.Close()
		return
	}
	ctrl, err := sess.AcceptStream() // client opens the control stream first
	if err != nil {
		sess.Close()
		return
	}
	// ctrl is unauthenticated until the hello checks out: bound the read so a
	// slow/silent peer can't pin this goroutine forever.
	ctrl.SetReadDeadline(time.Now().Add(10 * time.Second))
	hello, err := proto.ReadHello(ctrl)
	if err != nil || subtle.ConstantTimeCompare([]byte(hello.Token), []byte(s.cfg.Token)) != 1 {
		proto.WriteAck(ctrl, proto.HelloAck{OK: false, Error: "unauthorized"})
		sess.Close()
		return
	}
	ctrl.SetReadDeadline(time.Time{}) // authorized: no deadline for the life of the session

	client := &ClientReg{ID: hello.ClientID, Session: sess, Services: map[string]string{}}
	endpoints := map[string]string{}
	for _, svc := range hello.Services {
		ln, port, err := s.listen()
		if err != nil {
			proto.WriteAck(ctrl, proto.HelloAck{OK: false, Error: "no free port"})
			client.Close()
			return
		}
		client.Services[svc.Name] = svc.Local
		client.AddListener(ln)
		endpoints[svc.Name] = fmt.Sprintf("%s:%d", s.cfg.PublicHost, port)
		go s.servePublic(sess, svc.Name, ln)
	}
	if old := s.reg.Put(client); old != nil {
		old.Close() // client_id reconnected: tear down the stale registration
	}
	proto.WriteAck(ctrl, proto.HelloAck{OK: true, Endpoints: endpoints})
	slog.Info("client registered", "id", hello.ClientID, "endpoints", endpoints)

	// Block until the session dies, then clean up.
	<-sess.CloseChan()
	s.reg.Delete(hello.ClientID, client)
	client.Close()
	slog.Info("client gone", "id", hello.ClientID)
}

// listen binds the first free public port in [PortMin,PortMax], scanning from
// PortMin on every call so a port freed by a torn-down client is reclaimed
// (a just-closed TCP port re-binds immediately; Go sets SO_REUSEADDR). There's
// no shared mutable state, so concurrent callers need no lock: a collision on
// bind just means the loser continues scanning.
func (s *Server) listen() (net.Listener, int, error) {
	for p := s.cfg.PortMin; p <= s.cfg.PortMax; p++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", p))
		if err == nil {
			return ln, p, nil
		}
	}
	return nil, 0, fmt.Errorf("no free port in [%d,%d]", s.cfg.PortMin, s.cfg.PortMax)
}

// servePublic accepts visitors on a public port and splices each to a fresh
// yamux stream tagged with the service name.
func (s *Server) servePublic(sess *yamux.Session, service string, ln net.Listener) {
	for {
		vis, err := ln.Accept()
		if err != nil {
			return // listener closed on cleanup
		}
		go func() {
			st, err := sess.OpenStream()
			if err != nil {
				vis.Close()
				return
			}
			if err := proto.WriteStreamHeader(st, service); err != nil {
				st.Close()
				vis.Close()
				return
			}
			tunnel.Pipe(vis, st)
		}()
	}
}
