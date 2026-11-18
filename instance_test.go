package main

import (
	"net"
	"testing"
	"time"
)

func TestInstanceHello(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	quit := make(chan struct{})
	go serveInstance(l, "", func() { close(quit) })
	addr := l.Addr().String()

	for _, v := range []string{version, "0.0.1", "nightly"} {
		if got := hello(addr, v, ""); got != "stay" {
			t.Fatalf("hello from %s = %q, want stay", v, got)
		}
	}
	if got := hello(addr, "99.0.0", ""); got != "quit" {
		t.Fatalf("hello from a newer version = %q, want quit", got)
	}
	select {
	case <-quit:
	case <-time.After(time.Second):
		t.Fatal("did not quit for a newer version")
	}
}

func TestInstanceSecretRequired(t *testing.T) {
	dir := t.TempDir()
	secret := writeInstanceSecret(dir)
	if secret == "" || readInstanceSecret(dir) != secret {
		t.Fatalf("secret round-trip failed: %q / %q", secret, readInstanceSecret(dir))
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	quit := make(chan struct{})
	go serveInstance(l, secret, func() { close(quit) })
	addr := l.Addr().String()

	for _, bad := range []string{"", "wrong-secret"} {
		if got := hello(addr, "99.0.0", bad); got != "stay" {
			t.Fatalf("unauthorized hello (%q) = %q, want stay", bad, got)
		}
	}
	if got := hello(addr, "99.0.0", secret); got != "quit" {
		t.Fatalf("authorized hello = %q, want quit", got)
	}
	select {
	case <-quit:
	case <-time.After(time.Second):
		t.Fatal("did not quit for an authorized newer version")
	}

	removeInstanceSecret(dir)
	if got := readInstanceSecret(dir); got != "" {
		t.Fatalf("secret not removed: %q", got)
	}
}

func TestInstanceHelloUnanswered(t *testing.T) {
	// Versions before 0.3.3 hold the port and never answer.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if got := hello(l.Addr().String(), version, ""); got != "" {
		t.Fatalf("hello = %q from a switcher that does not answer", got)
	}
}
