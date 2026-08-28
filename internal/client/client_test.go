package client

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/mannk98/gotun/internal/proto"
	"github.com/mannk98/gotun/internal/tunnel"
)

// dialerFunc adapts a func to tunnel.Dialer.
type dialerFunc func(context.Context) (net.Conn, error)

func (f dialerFunc) Dial(ctx context.Context) (net.Conn, error) { return f(ctx) }

func TestClientServesStreamToLocal(t *testing.T) {
	// local echo service the client should dial on demand
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
	c := New(Config{
		Dialer:   dialerFunc(func(context.Context) (net.Conn, error) { return cSide, nil }),
		Token:    "t",
		ClientID: "c1",
		Services: []proto.Service{{Name: "echo", Local: echoLn.Addr().String()}},
	})
	go c.connectOnce(context.Background())

	// Play the server side over sSide.
	sess, _ := tunnel.ServerSession(sSide)
	ctrl, _ := sess.AcceptStream()
	hello, err := proto.ReadHello(ctrl)
	if err != nil || hello.ClientID != "c1" || hello.Services[0].Name != "echo" {
		t.Fatalf("hello: %+v err=%v", hello, err)
	}
	proto.WriteAck(ctrl, proto.HelloAck{OK: true, Endpoints: map[string]string{"echo": "h:1"}})

	// Open a data stream tagged "echo"; the client must dial local echo and splice.
	st, _ := sess.OpenStream()
	proto.WriteStreamHeader(st, "echo")
	st.Write([]byte("abc"))
	got := make([]byte, 3)
	st.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(st, got); err != nil || string(got) != "abc" {
		t.Fatalf("round-trip got=%q err=%v", got, err)
	}
}

// connectResult captures connectOnce's return tuple from a background goroutine.
type connectResult struct {
	connected bool
	err       error
}

func TestConnectOnceReportsConnected(t *testing.T) {
	t.Run("handshake ok", func(t *testing.T) {
		cSide, sSide := net.Pipe()
		c := New(Config{
			Dialer:   dialerFunc(func(context.Context) (net.Conn, error) { return cSide, nil }),
			Token:    "t",
			ClientID: "c1",
		})

		done := make(chan connectResult, 1)
		go func() {
			connected, err := c.connectOnce(context.Background())
			done <- connectResult{connected, err}
		}()

		sess, _ := tunnel.ServerSession(sSide)
		ctrl, _ := sess.AcceptStream()
		if _, err := proto.ReadHello(ctrl); err != nil {
			t.Fatalf("ReadHello: %v", err)
		}
		proto.WriteAck(ctrl, proto.HelloAck{OK: true})

		// Prove the ack was actually delivered (not just enqueued) before
		// tearing the session down: open a stream for an unregistered
		// service. The client only closes it from inside handleStream,
		// which only runs once connectOnce is past the ack check.
		st, _ := sess.OpenStream()
		proto.WriteStreamHeader(st, "unregistered")
		st.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, err := st.Read(make([]byte, 1)); err == nil {
			t.Fatal("expected client to close the stream for an unregistered service")
		} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
			t.Fatalf("timed out waiting for client to close stream (ack not delivered?): %v", err)
		}

		sess.Close() // now safe: drop the session so connectOnce's accept loop returns

		select {
		case r := <-done:
			if !r.connected {
				t.Fatalf("connected = false, want true (err=%v)", r.err)
			}
			if r.err == nil {
				t.Fatal("err = nil, want non-nil once the session drops")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("connectOnce did not return after session close")
		}
	})

	t.Run("handshake rejected", func(t *testing.T) {
		cSide, sSide := net.Pipe()
		c := New(Config{
			Dialer:   dialerFunc(func(context.Context) (net.Conn, error) { return cSide, nil }),
			Token:    "bad",
			ClientID: "c1",
		})

		done := make(chan connectResult, 1)
		go func() {
			connected, err := c.connectOnce(context.Background())
			done <- connectResult{connected, err}
		}()

		sess, _ := tunnel.ServerSession(sSide)
		ctrl, _ := sess.AcceptStream()
		if _, err := proto.ReadHello(ctrl); err != nil {
			t.Fatalf("ReadHello: %v", err)
		}
		proto.WriteAck(ctrl, proto.HelloAck{OK: false, Error: "bad token"})

		select {
		case r := <-done:
			if r.connected {
				t.Fatal("connected = true, want false")
			}
			if _, ok := r.err.(*authError); !ok {
				t.Fatalf("err = %v (%T), want *authError", r.err, r.err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("connectOnce did not return after rejected ack")
		}
	})
}
