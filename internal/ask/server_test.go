package ask

import (
	"encoding/json"
	"net"
	"testing"
	"time"
)

func TestServerHandlesVerdictRequest(t *testing.T) {
	mockDialog := func(process, domain string, port int, project string) string {
		return VerdictAllowOnce
	}

	srv := NewServer("test-project", "", mockDialog)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	go srv.Serve(listener)

	conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	req := VerdictRequest{Domain: "evil.com", Port: 443, Process: "npm", IP: "1.2.3.4"}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		t.Fatal(err)
	}

	var resp VerdictResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if resp.Verdict != VerdictAllowOnce {
		t.Errorf("got verdict %q, want %q", resp.Verdict, VerdictAllowOnce)
	}
}

func TestServerCachesPreviousVerdicts(t *testing.T) {
	callCount := 0
	mockDialog := func(process, domain string, port int, project string) string {
		callCount++
		return VerdictAlwaysAllow
	}

	srv := NewServer("test-project", "", mockDialog)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	go srv.Serve(listener)

	for i := 0; i < 2; i++ {
		conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
		if err != nil {
			t.Fatal(err)
		}

		req := VerdictRequest{Domain: "evil.com", Port: 443, Process: "npm"}
		json.NewEncoder(conn).Encode(req)

		var resp VerdictResponse
		json.NewDecoder(conn).Decode(&resp)
		conn.Close()

		if resp.Verdict != VerdictAlwaysAllow {
			t.Errorf("request %d: got %q, want %q", i, resp.Verdict, VerdictAlwaysAllow)
		}
	}

	if callCount != 1 {
		t.Errorf("dialog shown %d times, expected 1 (cached)", callCount)
	}
}

func TestServerAllowOnceNotCached(t *testing.T) {
	callCount := 0
	mockDialog := func(process, domain string, port int, project string) string {
		callCount++
		return VerdictAllowOnce
	}

	srv := NewServer("test-project", "", mockDialog)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go srv.Serve(listener)

	// Send same domain twice
	for i := 0; i < 2; i++ {
		conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		req := VerdictRequest{Domain: "evil.com", Port: 443, Process: "npm"}
		json.NewEncoder(conn).Encode(req)
		var resp VerdictResponse
		json.NewDecoder(conn).Decode(&resp)
		conn.Close()
	}

	// allow-once should NOT be cached, so dialog shown twice
	if callCount != 2 {
		t.Errorf("dialog shown %d times, expected 2 (allow-once not cached)", callCount)
	}
}

func TestServerBlockVerdictCached(t *testing.T) {
	callCount := 0
	mockDialog := func(process, domain string, port int, project string) string {
		callCount++
		return VerdictBlock
	}

	srv := NewServer("test-project", "", mockDialog)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	go srv.Serve(listener)

	for i := 0; i < 2; i++ {
		conn, _ := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
		req := VerdictRequest{Domain: "evil.com", Port: 443, Process: "npm"}
		json.NewEncoder(conn).Encode(req)
		var resp VerdictResponse
		json.NewDecoder(conn).Decode(&resp)
		conn.Close()
	}

	if callCount != 1 {
		t.Errorf("dialog shown %d times, expected 1 (block cached for session)", callCount)
	}
}

func TestServerSequentialDialogs(t *testing.T) {
	dialogOrder := []string{}
	mockDialog := func(process, domain string, port int, project string) string {
		dialogOrder = append(dialogOrder, domain)
		return VerdictAllowOnce
	}

	srv := NewServer("test-project", "", mockDialog)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	go srv.Serve(listener)

	domains := []string{"a.com", "b.com", "c.com"}
	for _, domain := range domains {
		conn, _ := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
		req := VerdictRequest{Domain: domain, Port: 443, Process: "npm"}
		json.NewEncoder(conn).Encode(req)
		var resp VerdictResponse
		json.NewDecoder(conn).Decode(&resp)
		conn.Close()
	}

	if len(dialogOrder) != 3 {
		t.Errorf("expected 3 dialogs, got %d", len(dialogOrder))
	}
}

func TestServerDifferentPortsGetSeparateVerdicts(t *testing.T) {
	callCount := 0
	mockDialog := func(process, domain string, port int, project string) string {
		callCount++
		return VerdictBlock
	}

	srv := NewServer("test-project", "", mockDialog)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go srv.Serve(listener)

	// Block domain on port 443
	conn, _ := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	req := VerdictRequest{Domain: "example.com", Port: 443, Process: "npm"}
	json.NewEncoder(conn).Encode(req)
	var resp VerdictResponse
	json.NewDecoder(conn).Decode(&resp)
	conn.Close()

	// Same domain, different port should show a new dialog
	conn, _ = net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	req = VerdictRequest{Domain: "example.com", Port: 80, Process: "npm"}
	json.NewEncoder(conn).Encode(req)
	json.NewDecoder(conn).Decode(&resp)
	conn.Close()

	if callCount != 2 {
		t.Errorf("dialog shown %d times, expected 2 (different ports get separate verdicts)", callCount)
	}
}

func TestServerFallsBackToIPWhenNoDomain(t *testing.T) {
	var receivedDomain string
	mockDialog := func(process, domain string, port int, project string) string {
		receivedDomain = domain
		return VerdictBlock
	}

	srv := NewServer("test-project", "", mockDialog)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	go srv.Serve(listener)

	conn, _ := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	req := VerdictRequest{Domain: "", Port: 443, Process: "npm", IP: "93.184.216.34"}
	json.NewEncoder(conn).Encode(req)
	var resp VerdictResponse
	json.NewDecoder(conn).Decode(&resp)
	conn.Close()

	if receivedDomain != "93.184.216.34" {
		t.Errorf("expected dialog to show IP when domain is empty, got %q", receivedDomain)
	}
}
