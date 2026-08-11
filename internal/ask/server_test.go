package ask

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testServerAuthKey = func() AuthKey {
	key, err := ParseAuthKey("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		panic(err)
	}
	return key
}()

func sendTestVerdictRequest(t *testing.T, conn net.Conn, req VerdictRequest) VerdictRequest {
	t.Helper()
	if err := AuthenticateRequest(testServerAuthKey, &req); err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		t.Fatal(err)
	}
	return req
}

func TestServerHandlesVerdictRequest(t *testing.T) {
	mockDialog := func(process, domain string, port int, project string) string {
		return VerdictAllowOnce
	}

	srv := NewServer("test-project", "", testServerAuthKey, mockDialog)
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
	authenticatedReq := sendTestVerdictRequest(t, conn, req)

	var resp VerdictResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if resp.Verdict != VerdictAllowOnce {
		t.Errorf("got verdict %q, want %q", resp.Verdict, VerdictAllowOnce)
	}
	if !VerifyResponse(testServerAuthKey, authenticatedReq, resp) {
		t.Fatal("server response was not authenticated to the request")
	}
}

func TestServerRejectsUnauthenticatedRequestBeforeDialogOrConfigWrite(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), ".watermelon.toml")
	original := []byte("[network]\nallow = []\n")
	if err := os.WriteFile(configPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	dialogCalls := 0
	srv := NewServer("test-project", configPath, testServerAuthKey, func(_, _ string, _ int, _ string) string {
		dialogCalls++
		return VerdictAlwaysAllow
	})

	unauthenticated := VerdictRequest{Domain: "evil.com", Port: 443, Process: "npm", IP: "1.2.3.4"}
	payload, err := json.Marshal(unauthenticated)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exchangeRawVerdictRequest(t, srv, append(payload, '\n')); err == nil {
		t.Fatal("unauthenticated request unexpectedly received a response")
	}
	if dialogCalls != 0 {
		t.Fatalf("unauthenticated request showed %d dialogs", dialogCalls)
	}
	if got, err := os.ReadFile(configPath); err != nil || !bytes.Equal(got, original) {
		t.Fatalf("unauthenticated request changed config: data=%q err=%v", got, err)
	}

	valid := VerdictRequest{Domain: "evil.com", Port: 443, Process: "npm", IP: "1.2.3.4"}
	if err := AuthenticateRequest(testServerAuthKey, &valid); err != nil {
		t.Fatal(err)
	}
	validPayload, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := exchangeRawVerdictRequest(t, srv, append(validPayload, '\n'))
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyResponse(testServerAuthKey, valid, resp) || resp.Verdict != VerdictAlwaysAllow {
		t.Fatalf("authenticated response = %+v", resp)
	}
	if dialogCalls != 1 {
		t.Fatalf("authenticated request showed %d dialogs, want 1", dialogCalls)
	}
	if got, err := os.ReadFile(configPath); err != nil || !strings.Contains(string(got), "evil.com") {
		t.Fatalf("authenticated always-allow did not update config: data=%q err=%v", got, err)
	}
}

func TestServerRejectsMalformedAndSemanticallyInvalidRequests(t *testing.T) {
	dialogCalls := 0
	srv := NewServer("test-project", "", testServerAuthKey, func(_, _ string, _ int, _ string) string {
		dialogCalls++
		return VerdictAllowOnce
	})

	valid := VerdictRequest{Domain: "example.com", Port: 443, Process: "npm", IP: "93.184.216.34"}
	if err := AuthenticateRequest(testServerAuthKey, &valid); err != nil {
		t.Fatal(err)
	}
	validJSON, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	unknownField := append(append([]byte(nil), validJSON[:len(validJSON)-1]...), []byte(",\"extra\":true}\n")...)
	requests := [][]byte{
		unknownField,
		append(bytes.Repeat([]byte("x"), maxVerdictRequestBytes+1), '\n'),
	}
	for i, payload := range requests {
		if _, err := exchangeRawVerdictRequest(t, srv, payload); err == nil {
			t.Errorf("malformed request %d unexpectedly received a response", i)
		}
	}

	semanticCases := []VerdictRequest{
		{Domain: "example.com", Port: 0, Process: "npm", IP: "93.184.216.34"},
		{Domain: "bad domain", Port: 443, Process: "npm", IP: "93.184.216.34"},
		{Port: 443, Process: "npm"},
		{Domain: "example.com", Port: 443, Process: "bad\nprocess", IP: "93.184.216.34"},
		{Domain: "example.com", Port: 443, Process: "npm", IP: "not-an-ip"},
	}
	for i, req := range semanticCases {
		if err := AuthenticateRequest(testServerAuthKey, &req); err != nil {
			t.Fatal(err)
		}
		payload, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := exchangeRawVerdictRequest(t, srv, append(payload, '\n')); err == nil {
			t.Errorf("semantic request %d unexpectedly received a response", i)
		}
	}
	if dialogCalls != 0 {
		t.Fatalf("invalid requests showed %d dialogs", dialogCalls)
	}
}

func TestServerReadDeadlineClosesSlowRequest(t *testing.T) {
	srv := NewServer("test-project", "", testServerAuthKey, func(_, _ string, _ int, _ string) string {
		t.Fatal("slow request reached dialog")
		return VerdictBlock
	})
	srv.readTimeout = 20 * time.Millisecond
	serverConn, clientConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		srv.handleConn(serverConn)
		close(done)
	}()
	if _, err := clientConn.Write([]byte("{")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("slow request was not closed by its read deadline")
	}
	_ = clientConn.Close()
}

func TestServerReplayWindowIsBoundedAndRejectsRecentReplay(t *testing.T) {
	srv := NewServer("test-project", "", testServerAuthKey, nil)
	srv.nonceLimit = 2
	srv.nonceOrder = make([]string, 0, srv.nonceLimit)
	if !srv.markNonce("a") || srv.markNonce("a") {
		t.Fatal("recent nonce replay was not rejected")
	}
	if !srv.markNonce("b") || !srv.markNonce("c") {
		t.Fatal("new nonce was rejected")
	}
	if len(srv.seenNonces) != srv.nonceLimit {
		t.Fatalf("replay map size = %d, want %d", len(srv.seenNonces), srv.nonceLimit)
	}
	if !srv.markNonce("a") {
		t.Fatal("nonce outside the bounded replay window was not evicted")
	}
	if srv.markNonce("c") {
		t.Fatal("nonce still inside the replay window was accepted twice")
	}
}

func TestServerNormalizesInvalidDialogVerdictToAuthenticatedBlock(t *testing.T) {
	srv := NewServer("test-project", "", testServerAuthKey, func(_, _ string, _ int, _ string) string {
		return "unexpected-allow"
	})
	req := VerdictRequest{Domain: "example.com", Port: 443, Process: "npm", IP: "93.184.216.34"}
	if err := AuthenticateRequest(testServerAuthKey, &req); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := exchangeRawVerdictRequest(t, srv, append(payload, '\n'))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Verdict != VerdictBlock || !VerifyResponse(testServerAuthKey, req, resp) {
		t.Fatalf("invalid dialog result produced response %+v, want authenticated block", resp)
	}
}

func TestServerLimitsConcurrentConnectionsPerRemote(t *testing.T) {
	srv := NewServer("test-project", "", testServerAuthKey, nil)
	serverConns := make([]net.Conn, 0, maxConnectionsPerIP)
	clientConns := make([]net.Conn, 0, maxConnectionsPerIP)
	for range maxConnectionsPerIP {
		serverConn, clientConn := net.Pipe()
		if !srv.acquireConnection(serverConn) {
			t.Fatal("connection below per-remote limit was rejected")
		}
		serverConns = append(serverConns, serverConn)
		clientConns = append(clientConns, clientConn)
	}
	overLimitServer, overLimitClient := net.Pipe()
	if srv.acquireConnection(overLimitServer) {
		t.Fatal("connection above per-remote limit was accepted")
	}
	srv.releaseConnection(serverConns[0])
	if !srv.acquireConnection(overLimitServer) {
		t.Fatal("connection slot was not released")
	}
	srv.releaseConnection(overLimitServer)
	_ = overLimitServer.Close()
	_ = overLimitClient.Close()
	for i := 1; i < len(serverConns); i++ {
		srv.releaseConnection(serverConns[i])
	}
	for _, conn := range append(serverConns, clientConns...) {
		_ = conn.Close()
	}
}

func exchangeRawVerdictRequest(t *testing.T, srv *Server, payload []byte) (VerdictResponse, error) {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		srv.handleConn(serverConn)
		close(done)
	}()
	defer clientConn.Close()
	if err := clientConn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := clientConn.Write(payload); err != nil {
		return VerdictResponse{}, err
	}
	var resp VerdictResponse
	err := json.NewDecoder(clientConn).Decode(&resp)
	<-done
	return resp, err
}

func TestServerCachesPreviousVerdicts(t *testing.T) {
	callCount := 0
	mockDialog := func(process, domain string, port int, project string) string {
		callCount++
		return VerdictAlwaysAllow
	}

	srv := NewServer("test-project", "", testServerAuthKey, mockDialog)
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
		sendTestVerdictRequest(t, conn, req)

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

	srv := NewServer("test-project", "", testServerAuthKey, mockDialog)
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
		sendTestVerdictRequest(t, conn, req)
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

	srv := NewServer("test-project", "", testServerAuthKey, mockDialog)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	go srv.Serve(listener)

	for i := 0; i < 2; i++ {
		conn, _ := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
		req := VerdictRequest{Domain: "evil.com", Port: 443, Process: "npm"}
		sendTestVerdictRequest(t, conn, req)
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

	srv := NewServer("test-project", "", testServerAuthKey, mockDialog)
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
		sendTestVerdictRequest(t, conn, req)
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

	srv := NewServer("test-project", "", testServerAuthKey, mockDialog)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go srv.Serve(listener)

	// Block domain on port 443
	conn, _ := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	req := VerdictRequest{Domain: "example.com", Port: 443, Process: "npm"}
	sendTestVerdictRequest(t, conn, req)
	var resp VerdictResponse
	json.NewDecoder(conn).Decode(&resp)
	conn.Close()

	// Same domain, different port should show a new dialog
	conn, _ = net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	req = VerdictRequest{Domain: "example.com", Port: 80, Process: "npm"}
	sendTestVerdictRequest(t, conn, req)
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

	srv := NewServer("test-project", "", testServerAuthKey, mockDialog)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	go srv.Serve(listener)

	conn, _ := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	req := VerdictRequest{Domain: "", Port: 443, Process: "npm", IP: "93.184.216.34"}
	sendTestVerdictRequest(t, conn, req)
	var resp VerdictResponse
	json.NewDecoder(conn).Decode(&resp)
	conn.Close()

	if receivedDomain != "93.184.216.34" {
		t.Errorf("expected dialog to show IP when domain is empty, got %q", receivedDomain)
	}
}
