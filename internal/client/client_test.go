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
