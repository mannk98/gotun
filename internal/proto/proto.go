// Package proto is the gotun wire protocol: line-framed JSON control messages
// (Hello/HelloAck) and a one-line stream header naming the target service.
package proto

import (
	"encoding/json"
	"errors"
	"io"
)

// maxLineLen bounds Hello/Ack/stream-header lines read off an unauthenticated
// yamux stream, so a slow/silent peer can't grow readLine's buffer without limit.
const maxLineLen = 64 << 10 // 64 KiB

type Service struct {
	Name  string `json:"name"`
	Local string `json:"local"`
}

type Hello struct {
	Token    string    `json:"token"`
	ClientID string    `json:"client_id"`
	Services []Service `json:"services"`
}

type HelloAck struct {
	OK        bool              `json:"ok"`
	Error     string            `json:"error,omitempty"`
	Endpoints map[string]string `json:"endpoints,omitempty"`
}

func writeJSONLine(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}

// readLine reads bytes up to and including '\n', returning the content without
// the '\n'. It reads one byte at a time so it never over-reads into the payload
// that follows on the same stream. It gives up once the line exceeds
// maxLineLen, so a slow/silent peer on an unauthenticated stream can't pin
// the reader or grow buf without bound.
func readLine(r io.Reader) ([]byte, error) {
	var buf []byte
	b := make([]byte, 1)
	for {
		n, err := r.Read(b)
		if n > 0 {
			if b[0] == '\n' {
				return buf, nil
			}
			if len(buf) >= maxLineLen {
				return buf, errors.New("proto: line too long")
			}
			buf = append(buf, b[0])
		}
		if err != nil {
			return buf, err
		}
	}
}

func WriteHello(w io.Writer, h Hello) error  { return writeJSONLine(w, h) }
func WriteAck(w io.Writer, a HelloAck) error { return writeJSONLine(w, a) }

func ReadHello(r io.Reader) (Hello, error) {
	line, err := readLine(r)
	if err != nil {
		return Hello{}, err
	}
	var h Hello
	return h, json.Unmarshal(line, &h)
}

func ReadAck(r io.Reader) (HelloAck, error) {
	line, err := readLine(r)
	if err != nil {
		return HelloAck{}, err
	}
	var a HelloAck
	return a, json.Unmarshal(line, &a)
}

func WriteStreamHeader(w io.Writer, service string) error {
	_, err := io.WriteString(w, service+"\n")
	return err
}

func ReadStreamHeader(r io.Reader) (string, error) {
	line, err := readLine(r)
	return string(line), err
}
