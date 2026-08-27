package tunnel

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"

	"github.com/coder/websocket"
)

// --- dialer ---

type wssDialer struct {
	url          string
	cfg          *tls.Config
	proxyFromEnv bool
}

func NewWSSDialer(url string, cfg *tls.Config, proxyFromEnv bool) Dialer {
	return &wssDialer{url, cfg, proxyFromEnv}
}

func (d *wssDialer) Dial(ctx context.Context) (net.Conn, error) {
	tr := &http.Transport{TLSClientConfig: d.cfg}
	if d.proxyFromEnv {
		tr.Proxy = http.ProxyFromEnvironment // HTTPS_PROXY -> restricted egress
	}
	c, _, err := websocket.Dial(ctx, d.url, &websocket.DialOptions{
		HTTPClient: &http.Client{Transport: tr},
	})
	if err != nil {
		return nil, err
	}
	// A single tunnel carries a whole yamux session -> no per-message size cap.
	c.SetReadLimit(-1)
	return websocket.NetConn(context.Background(), c, websocket.MessageBinary), nil
}

// --- listener: an http.Server that upgrades `path` and hands conns to Accept() ---

type wssListener struct {
	ln    net.Listener
	srv   *http.Server
	conns chan net.Conn
}

func NewWSSListener(addr, path string, cfg *tls.Config) (Listener, error) {
	base, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	l := &wssListener{ln: base, conns: make(chan net.Conn, 16)}
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		c.SetReadLimit(-1)
		nc := websocket.NetConn(context.Background(), c, websocket.MessageBinary)
		l.conns <- nc
		// Block so the http handler (and the hijacked conn) stays alive until the
		// tunnel conn is closed by the relay.
		<-r.Context().Done()
	})
	l.srv = &http.Server{Handler: mux, TLSConfig: cfg}
	go func() {
		if cfg != nil {
			l.srv.ServeTLS(base, "", "") // certs come from TLSConfig
		} else {
			l.srv.Serve(base)
		}
	}()
	return l, nil
}

func (l *wssListener) Accept() (net.Conn, error) { return <-l.conns, nil }
func (l *wssListener) Addr() net.Addr            { return l.ln.Addr() }
func (l *wssListener) Close() error              { return l.srv.Close() }
