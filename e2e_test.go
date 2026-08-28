package gotun_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mannk98/gotun/internal/client"
	"github.com/mannk98/gotun/internal/proto"
	"github.com/mannk98/gotun/internal/relay"
	"github.com/mannk98/gotun/internal/tunnel"
)

// portSeq spreads each TestEndToEnd invocation over its own port range.
// ponytail: relay.Server/client.Client have no Close/Shutdown (proxyd/proxyc
// run for the process lifetime by design, same as every other task in this
// repo) so a finished run's session+listener goroutines are never torn down
// mid-process. Harmless for a single run, but under `-count=N` a later run's
// server would otherwise collide with an earlier run's still-open public
// port and get silently misrouted to its dead backend. Bump the range
// instead of adding shutdown plumbing no production caller needs.
var portSeq atomic.Int32

func cert(t *testing.T) (server, clientCfg *tls.Config) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "gotun.test"},
		DNSNames: []string{"gotun.test"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	leaf, _ := x509.ParseCertificate(der)
	c := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return &tls.Config{Certificates: []tls.Certificate{c}},
		&tls.Config{RootCAs: pool, ServerName: "gotun.test"}
}

func TestEndToEnd(t *testing.T) {
	sCfg, cCfg := cert(t)

	// real local echo "service"
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

	// server
	tln, err := tunnel.NewWSSListener("127.0.0.1:0", "/tunnel", sCfg)
	if err != nil {
		t.Fatalf("wss listen: %v", err)
	}
	defer tln.Close()
	portMin := 21000 + int(portSeq.Add(1)-1)*100 // disjoint range per invocation, see portSeq
	srv := relay.NewServer(relay.Config{Token: "t", PublicHost: "127.0.0.1", PortMin: portMin, PortMax: portMin + 100}, tln)
	go srv.Serve(context.Background())

	// client
	url := "wss://" + tln.Addr().String() + "/tunnel"
	c := client.New(client.Config{
		Dialer: tunnel.NewWSSDialer(url, cCfg, false),
		Token:  "t", ClientID: "e2e",
		Services: []proto.Service{{Name: "echo", Local: echoLn.Addr().String()}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	// The client logs its endpoint; for the test, poll the fixed range for the echo port.
	// The server allocates from PortMin upward -> portMin is the echo endpoint.
	pub := fmt.Sprintf("127.0.0.1:%d", portMin)
	roundtrip := func(msg string) {
		var conn net.Conn
		for i := 0; i < 100; i++ {
			conn, err = net.Dial("tcp", pub)
			if err == nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if err != nil {
			t.Fatalf("dial public: %v", err)
		}
		defer conn.Close()
		conn.Write([]byte(msg))
		got := make([]byte, len(msg))
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		if _, err := io.ReadFull(conn, got); err != nil || string(got) != msg {
			t.Fatalf("round-trip got=%q want=%q err=%v", got, msg, err)
		}
	}
	roundtrip("hello-1")
	roundtrip("hello-2") // second visitor => second concurrent stream on the same tunnel
}
