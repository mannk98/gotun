package relay

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/mannk98/gotun/internal/proto"
	"github.com/mannk98/gotun/internal/tunnel"
)

// pipeListener adapts a single net.Conn into a tunnel.Listener (one Accept).
type pipeListener struct{ ch chan net.Conn }

func (p *pipeListener) Accept() (net.Conn, error) {
	c, ok := <-p.ch
	if !ok {
		return nil, io.EOF
	}
	return c, nil
}
func (p *pipeListener) Addr() net.Addr { return &net.TCPAddr{} }
func (p *pipeListener) Close() error   { close(p.ch); return nil }

func TestServerRelaysToRegisteredService(t *testing.T) {
	// A fake local "service" the client will dial: an echo server.
	echoLn, _ := net.Listen("tcp", "127.0.0.1:0")
	defer echoLn.Close()
	go func() {
		for {
			c, err := echoLn.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(c, c); c.Close() }()
		}
	}()

	cSide, sSide := net.Pipe()
	pl := &pipeListener{ch: make(chan net.Conn, 1)}
	pl.ch <- sSide
	srv := NewServer(Config{Token: "t", PublicHost: "127.0.0.1", PortMin: 20000, PortMax: 20100}, pl)
	go srv.Serve(context.Background())

	// Minimal client: yamux over cSide, send hello, then serve streams by dialing echo.
	sess, _ := tunnel.ClientSession(cSide)
	ctrl, _ := sess.OpenStream()
	proto.WriteHello(ctrl, proto.Hello{Token: "t", ClientID: "c1",
		Services: []proto.Service{{Name: "echo", Local: echoLn.Addr().String()}}})
	ack, err := proto.ReadAck(ctrl)
	if err != nil || !ack.OK {
		t.Fatalf("ack: %+v err=%v", ack, err)
	}
	go func() {
		for {
			st, err := sess.AcceptStream()
			if err != nil {
				return
			}
			go func() {
				svc, _ := proto.ReadStreamHeader(st)
				if svc != "echo" {
					st.Close()
					return
				}
				local, err := net.Dial("tcp", echoLn.Addr().String())
				if err != nil {
					st.Close()
					return
				}
				tunnel.Pipe(st, local)
			}()
		}
	}()

	// Connect to the public endpoint the server allocated and echo through it.
	pub := ack.Endpoints["echo"]
	if pub == "" {
		t.Fatalf("no echo endpoint in ack: %+v", ack)
	}
	var conn net.Conn
	for i := 0; i < 50; i++ { // listener may need a beat to come up
		conn, err = net.Dial("tcp", pub)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dial public %s: %v", pub, err)
	}
	defer conn.Close()
	conn.Write([]byte("xyz"))
	got := make([]byte, 3)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(conn, got); err != nil || string(got) != "xyz" {
		t.Fatalf("round-trip got=%q err=%v", got, err)
	}
}

// TestHandleGivesUpWhenHelloNeverArrives guards the pre-auth handshake
// hardening: a client that opens the control stream but never sends a Hello
// must not pin handle()'s goroutine forever. The read deadline set around
// proto.ReadHello should fire and tear the whole tunnel session down.
func TestHandleGivesUpWhenHelloNeverArrives(t *testing.T) {
	cSide, sSide := net.Pipe()
	pl := &pipeListener{ch: make(chan net.Conn, 1)}
	pl.ch <- sSide
	srv := NewServer(Config{Token: "t", PublicHost: "127.0.0.1", PortMin: 20200, PortMax: 20300}, pl)
	go srv.Serve(context.Background())

	sess, err := tunnel.ClientSession(cSide)
	if err != nil {
		t.Fatalf("client session: %v", err)
	}
	// Open the control stream per protocol, but never write a Hello on it.
	if _, err := sess.OpenStream(); err != nil {
		t.Fatalf("open stream: %v", err)
	}

	deadline := time.Now().Add(12 * time.Second)
	for !sess.IsClosed() {
		if time.Now().After(deadline) {
			t.Fatal("server did not give up on a hello-less control stream within 12s (deadline not enforced?)")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
