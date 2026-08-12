//go:build linux

package main

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/saeta-eth/watermelon/internal/ask"
)

type temporaryNFQueueError struct{}

func (temporaryNFQueueError) Error() string   { return "temporary nfqueue failure" }
func (temporaryNFQueueError) Timeout() bool   { return false }
func (temporaryNFQueueError) Temporary() bool { return true }

func TestVerdictDestinationDoesNotReverseLookupDirectIP(t *testing.T) {
	cache := newDNSAttributionCache(10, time.Hour)
	if got := cache.Destination("203.0.113.7"); got != "203.0.113.7" {
		t.Fatalf("direct-IP destination = %q, want IP without a blocking reverse lookup", got)
	}

	cache.Observe([]dnsMapping{{
		IP: "203.0.113.7", Domain: "packages.example.test", TTL: time.Minute,
	}})
	if got := cache.Destination("203.0.113.7"); got != "packages.example.test" {
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

	got, authenticated := askServer(listener.Addr().String(), key, ask.VerdictRequest{
		Domain: "example.com", Port: 443, Process: "npm", IP: "93.184.216.34",
	})
	if got != ask.VerdictAllowOnce || !authenticated {
		t.Fatalf("askServer() = (%q, %v), want authenticated allow-once", got, authenticated)
	}
}

func TestNFQueueReceiveErrorsContinueOnlyWhenTemporary(t *testing.T) {
	fatalErrors := make(chan error, 1)
	if got := handleNFQueueReceiveError("TCP", fatalErrors, temporaryNFQueueError{}); got != 0 {
		t.Fatalf("temporary error callback result = %d, want continue", got)
	}
	select {
	case err := <-fatalErrors:
		t.Fatalf("temporary error was propagated as fatal: %v", err)
	default:
	}

	permanent := errors.New("socket closed unexpectedly")
	if got := handleNFQueueReceiveError("DNS", fatalErrors, permanent); got == 0 {
		t.Fatal("permanent error callback requested another receive")
	}
	select {
	case err := <-fatalErrors:
		if !errors.Is(err, permanent) || !strings.Contains(err.Error(), "DNS nfqueue receive failed") {
			t.Fatalf("propagated error = %v, want wrapped DNS failure", err)
		}
	case <-time.After(time.Second):
		t.Fatal("permanent nfqueue failure was not propagated to the daemon")
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

	got, authenticated := askServer(listener.Addr().String(), key, ask.VerdictRequest{
		Domain: "example.com", Port: 443, Process: "npm", IP: "93.184.216.34",
	})
	if got != ask.VerdictBlock || authenticated {
		t.Fatalf("forged host response produced (%q, %v), want unauthenticated block", got, authenticated)
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
