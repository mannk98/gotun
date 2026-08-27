package tunnel

import (
	"context"
	"testing"
)

func TestWSSTransportRoundTrip(t *testing.T) {
	sCfg, cCfg := selfSigned(t)
	ln, err := NewWSSListener("127.0.0.1:0", "/tunnel", sCfg)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		b := make([]byte, 4)
		c.Read(b)
		c.Write(b)
	}()

	url := "wss://" + ln.Addr().String() + "/tunnel"
	d := NewWSSDialer(url, cCfg, false)
	conn, err := d.Dial(context.Background())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	conn.Write([]byte("pong"))
	got := make([]byte, 4)
	if _, err := conn.Read(got); err != nil || string(got) != "pong" {
		t.Fatalf("echo got=%q err=%v", got, err)
	}
}
