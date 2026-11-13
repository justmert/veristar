package main

import (
	"crypto/rand"
	"strings"
	"testing"
)

func TestReadable(t *testing.T) {
	if !readable([]byte("CONNECT {\"user\":\"gateway\",\"pass\":\"x\"}\r\nPUB web.events.a 12\r\n" + strings.Repeat("hello world ", 10))) {
		t.Fatal("nats protocol text must be readable")
	}
	b := make([]byte, 512)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	if readable(b) {
		t.Fatal("random bytes, as tls ciphertext, must not count as readable")
	}
	if readable([]byte("short")) {
		t.Fatal("too few bytes to judge")
	}
}

func TestPrefixes(t *testing.T) {
	if !isTLSRecord([]byte{0x16, 0x03, 0x01, 0x02}) || !isTLSRecord([]byte{0x17, 0x03, 0x03}) {
		t.Fatal("handshake and application data records")
	}
	if isTLSRecord([]byte("GET / HTTP/1.1")) || isTLSRecord([]byte("INFO {")) {
		t.Fatal("plain protocols are not tls")
	}
}
