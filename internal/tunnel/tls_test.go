package tunnel

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"testing"
)

// selfSigned returns a server tls.Config and a client tls.Config that trusts it.
func selfSigned(t *testing.T) (server, client *tls.Config) {
	t.Helper()
	cert := genCert(t) // defined in session_test.go within this package
	pool := x509.NewCertPool()
	pool.AddCert(cert.Leaf)
	return &tls.Config{Certificates: []tls.Certificate{cert}},
		&tls.Config{RootCAs: pool, ServerName: "gotun.test"}
}

func TestTLSTransportRoundTrip(t *testing.T) {
	sCfg, cCfg := selfSigned(t)
	ln, err := NewTLSListener("127.0.0.1:0", sCfg)
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
		c.Write(b) // echo
	}()

	d := NewTLSDialer(ln.Addr().String(), cCfg)
	conn, err := d.Dial(context.Background())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	conn.Write([]byte("ping"))
	got := make([]byte, 4)
	if _, err := conn.Read(got); err != nil || string(got) != "ping" {
		t.Fatalf("echo got=%q err=%v", got, err)
	}
}
