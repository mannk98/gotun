package tunnel

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"
)

// genCert makes an in-memory self-signed cert for "gotun.test" (CN + SAN),
// with Leaf populated so tests can trust it directly.
func genCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "gotun.test"},
		DNSNames:     []string{"gotun.test"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("cert: %v", err)
	}
	leaf, _ := x509.ParseCertificate(der)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
}

func TestSessionOpenAcceptEcho(t *testing.T) {
	a, b := net.Pipe()
	cs, err := ClientSession(a)
	if err != nil {
		t.Fatalf("client sess: %v", err)
	}
	ss, err := ServerSession(b)
	if err != nil {
		t.Fatalf("server sess: %v", err)
	}
	go func() {
		st, err := ss.AcceptStream()
		if err != nil {
			return
		}
		buf := make([]byte, 5)
		st.Read(buf)
		st.Write(buf)
		st.Close()
	}()
	st, err := cs.OpenStream()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	st.Write([]byte("hello"))
	got := make([]byte, 5)
	if _, err := st.Read(got); err != nil || string(got) != "hello" {
		t.Fatalf("got=%q err=%v", got, err)
	}
}
