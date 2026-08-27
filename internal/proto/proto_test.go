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
