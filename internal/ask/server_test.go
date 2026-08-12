package ask

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/saeta-eth/watermelon/internal/config"
)

var testServerAuthKey = func() AuthKey {
	key, err := ParseAuthKey("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		panic(err)
	}
	return key
}()

type temporaryAcceptError struct{}

func (temporaryAcceptError) Error() string   { return "temporary accept failure" }
func (temporaryAcceptError) Timeout() bool   { return false }
func (temporaryAcceptError) Temporary() bool { return true }

type scriptedAcceptResult struct {
	conn net.Conn
	err  error
}

type scriptedListener struct {
	mu      sync.Mutex
	results []scriptedAcceptResult
}

func (l *scriptedListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.results) == 0 {
		return nil, net.ErrClosed
	}
	result := l.results[0]
	l.results = l.results[1:]
	return result.conn, result.err
}

func (l *scriptedListener) Close() error   { return nil }
func (l *scriptedListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }

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

func TestServerRetriesTemporaryAcceptFailure(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	listener := &scriptedListener{results: []scriptedAcceptResult{
		{err: temporaryAcceptError{}},
		{conn: serverConn},
		{err: net.ErrClosed},
	}}
	var dialogCalls atomic.Int32
	srv := NewServer("test-project", "", testServerAuthKey, func(_, _ string, _ int, _ string) string {
		dialogCalls.Add(1)
		return VerdictAllowOnce
	})
	go srv.Serve(listener)

	if err := clientConn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	req := sendTestVerdictRequest(t, clientConn, VerdictRequest{
		Domain: "example.com", Port: 443, Process: "npm", IP: "93.184.216.34",
	})
	var resp VerdictResponse
	if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
		t.Fatalf("reading verdict after temporary accept failure: %v", err)
	}
	if dialogCalls.Load() != 1 || resp.Verdict != VerdictAllowOnce || !VerifyResponse(testServerAuthKey, req, resp) {
		t.Fatalf("dialog calls/response = %d/%+v, want one authenticated allow-once", dialogCalls.Load(), resp)
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

func TestServerCachesDestinationVerdictsAcrossProcesses(t *testing.T) {
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

	processes := []string{"npm", "python"}
	for i, process := range processes {
		conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
		if err != nil {
			t.Fatal(err)
		}

		req := VerdictRequest{Domain: "evil.com", Port: 443, Process: process}
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

func TestServerCachesVerdictsByLiteralEndpoint(t *testing.T) {
	var prompted []string
	srv := NewServer("test-project", "", testServerAuthKey, func(_, domain string, _ int, _ string) string {
		prompted = append(prompted, domain)
		return VerdictBlock
	})

	first := VerdictRequest{Domain: "example.com", Port: 443, Process: "npm", IP: "192.0.2.10"}
	if got := srv.getVerdict(first); got != VerdictBlock {
		t.Fatalf("first verdict = %q, want block", got)
	}
	// A different name at the same kernel endpoint reuses the endpoint verdict.
	alias := VerdictRequest{Domain: "alias.example", Port: 443, Process: "curl", IP: "192.0.2.10"}
	if got := srv.getVerdict(alias); got != VerdictBlock {
		t.Fatalf("alias verdict = %q, want cached block", got)
	}
	// The same informational hostname at a new IP is a distinct endpoint.
	moved := VerdictRequest{Domain: "example.com", Port: 443, Process: "npm", IP: "192.0.2.11"}
	if got := srv.getVerdict(moved); got != VerdictBlock {
		t.Fatalf("moved-host verdict = %q, want block", got)
	}

	want := []string{"example.com [192.0.2.10]", "example.com [192.0.2.11]"}
	if !reflect.DeepEqual(prompted, want) {
		t.Fatalf("prompted domains = %#v, want %#v", prompted, want)
	}
}

func TestServerAlwaysAllowNoticeDistinguishesRuntimeAndSavedScopes(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), ".watermelon.toml")
	initial := "[network]\nallow = []\n\n[network.process]\nnpm = [\"registry.npmjs.org\"]\n"
	if err := os.WriteFile(configPath, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}

	const recreateCommand = "watermelon destroy --name shared-dev --force && watermelon run --name shared-dev"
	var notices bytes.Buffer
	srv := NewServer(
		"test-project",
		configPath,
		testServerAuthKey,
		func(_, _ string, _ int, _ string) string { return VerdictAlwaysAllow },
		WithNoticeWriter(&notices),
		WithRecreateCommand(recreateCommand),
	)

	request := VerdictRequest{Domain: "example.com", Port: 443, Process: "npm", IP: "93.184.216.34"}
	if verdict := srv.getVerdict(request); verdict != VerdictAlwaysAllow {
		t.Fatalf("getVerdict() = %q, want %q", verdict, VerdictAlwaysAllow)
	}

	got := notices.String()
	for _, want := range []string{
		"Allowed TCP destination example.com [93.184.216.34]:443 for all processes in the current VM runtime.",
		`Saved global host-only rule "example.com" with no process, protocol, or port scope`,
		"managed DNS remains enforced",
		"After this Watermelon session finishes",
		"apply the saved rule to future VM sessions",
		"recreation removes VM-local state",
		recreateCommand,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("always-allow success notice missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, `Saved global host-only rule "example.com:443"`) {
		t.Errorf("success notice incorrectly claims that the saved global rule retains the runtime port:\n%s", got)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Network.Allow) != 1 || cfg.Network.Allow[0] != "example.com" {
		t.Fatalf("persisted global rules = %v, want only the bare prompted host", cfg.Network.Allow)
	}
	if got := cfg.Network.Process["npm"]; len(got) != 1 || got[0] != "registry.npmjs.org" {
		t.Fatalf("persisted process-scoped rules = %v, want original process policy unchanged", got)
	}
}

func TestServerAlwaysAllowSaveFailureReportsRuntimeOnly(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), ".watermelon.toml")
	original := []byte("[network\nallow = []\n")
	if err := os.WriteFile(configPath, original, 0600); err != nil {
		t.Fatal(err)
	}

	const recreateCommand = "watermelon destroy --force && watermelon run"
	var notices bytes.Buffer
	srv := NewServer(
		"test-project",
		configPath,
		testServerAuthKey,
		func(_, _ string, _ int, _ string) string { return VerdictAlwaysAllow },
		WithNoticeWriter(&notices),
		WithRecreateCommand(recreateCommand),
	)

	request := VerdictRequest{Domain: "example.com", Port: 8443, Process: "npm", IP: "93.184.216.34"}
	if verdict := srv.getVerdict(request); verdict != VerdictAlwaysAllow {
		t.Fatalf("getVerdict() = %q, want the current-runtime %q despite persistence failure", verdict, VerdictAlwaysAllow)
	}

	got := notices.String()
	for _, want := range []string{
		"Allowed TCP destination example.com [93.184.216.34]:8443 for all processes in the current VM runtime",
		"global host-only rule was not saved",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("always-allow failure notice missing %q:\n%s", want, got)
		}
	}
	for _, misleading := range []string{
		`Saved global host-only rule "example.com"`,
		"apply the saved rule",
		"future VM sessions",
		recreateCommand,
	} {
		if strings.Contains(got, misleading) {
			t.Errorf("always-allow failure notice incorrectly contains %q:\n%s", misleading, got)
		}
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, original) {
		t.Fatalf("failed persistence changed config: got %q, want %q", data, original)
	}
}

func TestServerSerializesDifferentDestinationDecisionsThroughNotices(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), ".watermelon.toml")
	if err := os.WriteFile(configPath, []byte("[network]\nallow = []\n"), 0600); err != nil {
		t.Fatal(err)
	}

	var active int32
	var concurrent atomic.Bool
	var notices bytes.Buffer
	srv := NewServer(
		"test-project",
		configPath,
		testServerAuthKey,
		func(_, _ string, _ int, _ string) string {
			if atomic.AddInt32(&active, 1) != 1 {
				concurrent.Store(true)
			}
			time.Sleep(10 * time.Millisecond)
			atomic.AddInt32(&active, -1)
			return VerdictAlwaysAllow
		},
		WithNoticeWriter(&notices),
		WithRecreateCommand("watermelon destroy --name test --force && watermelon run --name test"),
	)

	requests := []VerdictRequest{
		{Domain: "one.example", Port: 443, Process: "npm", IP: "192.0.2.1"},
		{Domain: "two.example", Port: 8443, Process: "python", IP: "192.0.2.2"},
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, request := range requests {
		request := request
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_ = srv.getVerdict(request)
		}()
	}
	close(start)
	wg.Wait()

	if concurrent.Load() {
		t.Fatal("different-destination dialogs overlapped")
	}
	for _, host := range []string{"one.example", "two.example"} {
		if !strings.Contains(notices.String(), `Saved global host-only rule "`+host+`"`) {
			t.Errorf("serialized notices missing %q:\n%s", host, notices.String())
		}
		data, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), `"`+host+`"`) {
			t.Errorf("serialized config missing %q:\n%s", host, data)
		}
	}
}
