package proto

import (
	"bytes"
	"testing"
)

func TestHelloRoundTrip(t *testing.T) {
	in := Hello{Token: "s3cr3t", ClientID: "abc", Services: []Service{
		{Name: "novnc", Local: "127.0.0.1:8080"},
		{Name: "ssh", Local: "127.0.0.1:22"},
	}}
	var buf bytes.Buffer
	if err := WriteHello(&buf, in); err != nil {
		t.Fatalf("WriteHello: %v", err)
	}
	got, err := ReadHello(&buf)
	if err != nil {
		t.Fatalf("ReadHello: %v", err)
	}
	if got.Token != in.Token || got.ClientID != in.ClientID || len(got.Services) != 2 || got.Services[1].Local != "127.0.0.1:22" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestAckRoundTrip(t *testing.T) {
	in := HelloAck{OK: true, Endpoints: map[string]string{"novnc": "h:7001"}}
	var buf bytes.Buffer
	if err := WriteAck(&buf, in); err != nil {
		t.Fatalf("WriteAck: %v", err)
	}
	got, err := ReadAck(&buf)
	if err != nil || !got.OK || got.Endpoints["novnc"] != "h:7001" {
		t.Fatalf("ack round-trip: %+v err=%v", got, err)
	}
}

// TestReadLineRejectsOversizedInput guards the pre-auth handshake hardening:
// a peer that never sends '\n' must not grow readLine's buffer without bound.
// The input is a finite bytes.Reader (so a broken cap would still terminate
// at EOF, just slowly and unbounded) — the real assertion is that readLine
// gives up at maxLineLen and does NOT consume the whole input, proving the
// cap fired instead of EOF ending the loop.
func TestReadLineRejectsOversizedInput(t *testing.T) {
	data := bytes.Repeat([]byte("x"), maxLineLen+1024) // no newline anywhere
	r := bytes.NewReader(data)
	_, err := readLine(r)
	if err == nil {
		t.Fatal("readLine: want error for a line over maxLineLen, got nil")
	}
	if r.Len() == 0 {
		t.Fatal("readLine consumed the entire input before erroring; want it to give up at maxLineLen, not at EOF")
	}
}

// TestReadHelloRejectsOversizedInput confirms the cap is wired into the
// Hello path specifically (not just the low-level readLine helper).
func TestReadHelloRejectsOversizedInput(t *testing.T) {
	data := bytes.Repeat([]byte("x"), maxLineLen+1024)
	if _, err := ReadHello(bytes.NewReader(data)); err == nil {
		t.Fatal("ReadHello: want error for a line over maxLineLen, got nil")
	}
}

func TestStreamHeaderRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteStreamHeader(&buf, "novnc"); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf.WriteString("PAYLOAD") // header read must NOT consume payload
	svc, err := ReadStreamHeader(&buf)
	if err != nil || svc != "novnc" {
		t.Fatalf("svc=%q err=%v", svc, err)
	}
	if rest := buf.String(); rest != "PAYLOAD" {
		t.Fatalf("payload consumed, rest=%q", rest)
	}
}
