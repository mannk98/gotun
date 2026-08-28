package tunnel

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"
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

// TestWSSListenerHandlerDoesNotLeakGoroutine is a best-effort leak guard for
// the upgrade-handler fix: before the fix, the handler blocked forever on
// <-r.Context().Done() (which a hijacked conn's closure never cancels), so
// every tunnel connection pinned one goroutine permanently. After the fix,
// the handler returns right after handing the conn to l.conns, so the
// goroutine count should settle back near baseline shortly after both ends
// of each connection close.
//
// It drives several connections (not just one): the leak this guards is
// exactly +1 goroutine per connection, which a single dial can't reliably
// separate from ordinary +/-1 runtime-goroutine noise. Confirmed against the
// pre-fix code with a goroutine-stack dump (net/http.(*conn).serve parked at
// wss.go's old `<-r.Context().Done()`) before writing this assertion.
func TestWSSListenerHandlerDoesNotLeakGoroutine(t *testing.T) {
	sCfg, cCfg := selfSigned(t)
	ln, err := NewWSSListener("127.0.0.1:0", "/tunnel", sCfg)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	// Let the listener's own long-lived goroutines (its Serve loop, etc.)
	// start up before baselining, so they don't get counted as "leaked".
	time.Sleep(50 * time.Millisecond)
	runtime.GC()
	base := runtime.NumGoroutine()

	url := "wss://" + ln.Addr().String() + "/tunnel"
	d := NewWSSDialer(url, cCfg, false)
	const n = 5 // >> slack, so a real +1/conn leak can't hide in the tolerance
	for i := 0; i < n; i++ {
		conn, err := d.Dial(context.Background())
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		server, err := ln.Accept()
		if err != nil {
			t.Fatalf("accept %d: %v", i, err)
		}
		// coder/websocket's Close performs a graceful close handshake and
		// blocks waiting (up to 5s) for the peer's close frame, so the two
		// ends must be closed concurrently -- closing them one after the
		// other would make the first Close() stall the whole 5s before the
		// peer ever replies.
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); conn.Close() }()
		go func() { defer wg.Done(); server.Close() }()
		wg.Wait()
	}

	// Poll briefly rather than sleeping a fixed amount, to avoid flakiness
	// in either direction (too short = false failure, too long = slow test).
	deadline := time.Now().Add(1 * time.Second)
	const slack = 2 // small allowance for unrelated runtime/GC goroutines
	for {
		runtime.GC()
		got := runtime.NumGoroutine()
		if got <= base+slack {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutine count did not settle after closing %d connections: base=%d got=%d (handler goroutine leak?)", n, base, got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
