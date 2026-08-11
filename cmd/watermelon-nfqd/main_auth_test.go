//go:build linux

package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/saeta-eth/watermelon/internal/ask"
)

func TestVerdictDestinationDoesNotReverseLookupDirectIP(t *testing.T) {
	var cache sync.Map
	if got := verdictDestination("203.0.113.7", &cache); got != "203.0.113.7" {
		t.Fatalf("direct-IP destination = %q, want IP without a blocking reverse lookup", got)
	}

	cache.Store("203.0.113.7", "packages.example.test")
	if got := verdictDestination("203.0.113.7", &cache); got != "packages.example.test" {
		t.Fatalf("DNS-cached destination = %q, want original hostname", got)
	}
}

func nfqdTestAuthKey(t *testing.T, encoded string) ask.AuthKey {
	t.Helper()
	key, err := ask.ParseAuthKey(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestAskServerAcceptsOnlyAuthenticatedHostResponse(t *testing.T) {
	key := nfqdTestAuthKey(t, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	srv := ask.NewServer("test-project", "", key, func(_, _ string, _ int, _ string) string {
		return ask.VerdictAllowOnce
	})
	go srv.Serve(listener)

	got := askServer(listener.Addr().String(), key, ask.VerdictRequest{
		Domain: "example.com", Port: 443, Process: "npm", IP: "93.184.216.34",
	})
	if got != ask.VerdictAllowOnce {
		t.Fatalf("askServer() = %q, want authenticated allow-once", got)
	}
}

func TestAskServerBlocksForgedAllowFromPortImpostor(t *testing.T) {
	key := nfqdTestAuthKey(t, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		var req ask.VerdictRequest
		if json.NewDecoder(conn).Decode(&req) != nil {
			return
		}
		_ = json.NewEncoder(conn).Encode(ask.VerdictResponse{
			Verdict: ask.VerdictAlwaysAllow,
			Nonce:   req.Nonce,
			MAC:     strings.Repeat("0", 64),
		})
	}()

	got := askServer(listener.Addr().String(), key, ask.VerdictRequest{
		Domain: "example.com", Port: 443, Process: "npm", IP: "93.184.216.34",
	})
	if got != ask.VerdictBlock {
		t.Fatalf("forged host response produced %q, want block", got)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("impostor server did not finish")
	}
}

func TestLoadAuthKeyFileRejectsNonRootOwnedFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("test requires a non-root test runner")
	}
	path := filepath.Join(t.TempDir(), "verdict-auth-key")
	if err := os.WriteFile(path, []byte("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAuthKeyFile(path); err == nil || !strings.Contains(err.Error(), "owned by root") {
		t.Fatalf("loadAuthKeyFile() error = %v, want root-owner rejection", err)
	}
}
