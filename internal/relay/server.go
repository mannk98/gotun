package relay

import (
	"context"
	"fmt"
	"log/slog"
	"net"

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
	cfg  Config
	ln   tunnel.Listener
	reg  *Registry
	next int
}

func NewServer(cfg Config, ln tunnel.Listener) *Server {
	return &Server{cfg: cfg, ln: ln, reg: NewRegistry(), next: cfg.PortMin}
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
	hello, err := proto.ReadHello(ctrl)
	if err != nil || hello.Token != s.cfg.Token {
		proto.WriteAck(ctrl, proto.HelloAck{OK: false, Error: "unauthorized"})
		sess.Close()
		return
	}

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
	s.reg.Delete(hello.ClientID)
	client.Close()
	slog.Info("client gone", "id", hello.ClientID)
}

// listen grabs the next free public port in [PortMin,PortMax].
func (s *Server) listen() (net.Listener, int, error) {
	for p := s.next; p <= s.cfg.PortMax; p++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", p))
		if err == nil {
			s.next = p + 1
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
