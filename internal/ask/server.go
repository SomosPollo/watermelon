package ask

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/saeta-eth/watermelon/internal/config"
)

const (
	maxVerdictRequestBytes = 4 << 10
	maxVerdictConnections  = 32
	maxConnectionsPerIP    = 4
	maxRequestDomainBytes  = 253
	maxRequestProcessBytes = 128
	verdictReplayWindow    = 4096
	verdictReadTimeout     = 5 * time.Second
	verdictWriteTimeout    = 5 * time.Second
)

// Server handles verdict requests from the VM-side nfqd daemon.
type Server struct {
	project      string
	configPath   string
	cache        *Cache
	dialog       DialogFunc
	dialogMu     sync.Mutex // ensures one dialog at a time
	authKey      AuthKey
	connections  chan struct{}
	connectionMu sync.Mutex
	remoteCounts map[string]int
	nonceMu      sync.Mutex
	seenNonces   map[string]struct{}
	nonceOrder   []string
	nonceCursor  int
	nonceLimit   int
	readTimeout  time.Duration
	writeTimeout time.Duration
}

// NewServer creates a verdict server.
// project is the project name shown in dialogs.
// configPath is the path to .watermelon.toml (for always-allow writes). Empty string disables TOML writes.
// authKey is the per-instance key shared only with the root-owned guest daemon.
// dialog is the function to show the verdict dialog. Pass nil to use the
// default host prompt.
func NewServer(project, configPath string, authKey AuthKey, dialog DialogFunc) *Server {
	if dialog == nil {
		dialog = ShowDialog
	}
	return &Server{
		project:      project,
		configPath:   configPath,
		cache:        NewCache(),
		dialog:       dialog,
		authKey:      authKey,
		connections:  make(chan struct{}, maxVerdictConnections),
		remoteCounts: make(map[string]int),
		seenNonces:   make(map[string]struct{}),
		nonceOrder:   make([]string, 0, verdictReplayWindow),
		nonceLimit:   verdictReplayWindow,
		readTimeout:  verdictReadTimeout,
		writeTimeout: verdictWriteTimeout,
	}
}

// Serve accepts connections on the listener and handles verdict requests.
func (s *Server) Serve(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return // listener closed
		}
		if s.acquireConnection(conn) {
			go func() {
				defer s.releaseConnection(conn)
				s.handleConn(conn)
			}()
		} else {
			// An exposed listener must not create an unbounded number of
			// goroutines, or let one remote peer occupy every slot, with slow
			// or idle unauthenticated connections.
			_ = conn.Close()
		}
	}
}

func (s *Server) acquireConnection(conn net.Conn) bool {
	select {
	case s.connections <- struct{}{}:
	default:
		return false
	}
	remote := remoteIPKey(conn.RemoteAddr())
	s.connectionMu.Lock()
	defer s.connectionMu.Unlock()
	if s.remoteCounts[remote] >= maxConnectionsPerIP {
		<-s.connections
		return false
	}
	s.remoteCounts[remote]++
	return true
}

func (s *Server) releaseConnection(conn net.Conn) {
	remote := remoteIPKey(conn.RemoteAddr())
	s.connectionMu.Lock()
	if s.remoteCounts[remote] <= 1 {
		delete(s.remoteCounts, remote)
	} else {
		s.remoteCounts[remote]--
	}
	s.connectionMu.Unlock()
	<-s.connections
}

func remoteIPKey(addr net.Addr) string {
	if tcpAddr, ok := addr.(*net.TCPAddr); ok {
		return tcpAddr.IP.String()
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err == nil {
		return host
	}
	return addr.String()
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()

	if err := conn.SetReadDeadline(time.Now().Add(s.readTimeout)); err != nil {
		return
	}
	req, err := decodeVerdictRequest(conn)
	if err != nil {
		return
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		return
	}

	// Authentication deliberately precedes semantic validation and every
	// cache, dialog, and config path. Invalid peers receive no signing oracle.
	if !VerifyRequest(s.authKey, req) || !validVerdictRequest(req) || !s.markNonce(req.Nonce) {
		return
	}

	verdict := s.getVerdict(req)
	resp, err := AuthenticateResponse(s.authKey, req, verdict)
	if err != nil {
		return
	}
	if err := conn.SetWriteDeadline(time.Now().Add(s.writeTimeout)); err != nil {
		return
	}
	_ = json.NewEncoder(conn).Encode(resp)
}

func decodeVerdictRequest(conn net.Conn) (VerdictRequest, error) {
	reader := bufio.NewReaderSize(conn, maxVerdictRequestBytes+1)
	line, isPrefix, err := reader.ReadLine()
	if err != nil {
		return VerdictRequest{}, err
	}
	if isPrefix || len(line) > maxVerdictRequestBytes {
		return VerdictRequest{}, errors.New("verdict request exceeds size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	var req VerdictRequest
	if err := decoder.Decode(&req); err != nil {
		return VerdictRequest{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return VerdictRequest{}, errors.New("verdict request contains trailing JSON")
		}
		return VerdictRequest{}, err
	}
	return req, nil
}

func validVerdictRequest(req VerdictRequest) bool {
	if req.Port < 1 || req.Port > 65535 || len(req.Domain) > maxRequestDomainBytes {
		return false
	}
	if req.Domain == "" && req.IP == "" {
		return false
	}
	if req.Domain != "" {
		rule, err := config.ParseNetworkRule(req.Domain)
		if err != nil || rule.Wildcard || rule.Port != 0 {
			return false
		}
	}
	if req.IP != "" {
		addr, err := netip.ParseAddr(req.IP)
		if err != nil || !addr.Is4() {
			return false
		}
	}
	if len(req.Process) > maxRequestProcessBytes || !utf8.ValidString(req.Process) {
		return false
	}
	for _, r := range req.Process {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

func (s *Server) markNonce(nonce string) bool {
	s.nonceMu.Lock()
	defer s.nonceMu.Unlock()
	if _, exists := s.seenNonces[nonce]; exists {
		return false
	}
	if s.nonceLimit <= 0 {
		return false
	}
	if len(s.nonceOrder) < s.nonceLimit {
		s.nonceOrder = append(s.nonceOrder, nonce)
	} else {
		expired := s.nonceOrder[s.nonceCursor]
		delete(s.seenNonces, expired)
		s.nonceOrder[s.nonceCursor] = nonce
		s.nonceCursor = (s.nonceCursor + 1) % s.nonceLimit
	}
	s.seenNonces[nonce] = struct{}{}
	return true
}

func (s *Server) getVerdict(req VerdictRequest) string {
	domain := req.Domain
	if domain == "" {
		domain = req.IP
	}

	cacheKey := fmt.Sprintf("%s:%d", domain, req.Port)

	// Check cache first
	if v, ok := s.cache.Get(cacheKey); ok {
		return v
	}

	// Check if another goroutine is already showing a dialog for this domain:port
	if ch := s.cache.MarkPending(cacheKey); ch != nil {
		<-ch // wait for the other dialog to complete
		if v, ok := s.cache.Get(cacheKey); ok {
			return v
		}
		// Not cached (e.g. allow-once) — re-enter to prompt again
		return s.getVerdict(req)
	}

	// We're the first — show dialog (one at a time)
	s.dialogMu.Lock()
	verdict := s.dialog(req.Process, domain, req.Port, s.project)
	s.dialogMu.Unlock()
	if !ValidVerdict(verdict) {
		verdict = VerdictBlock
	}

	// Cache block and always-allow for the session; allow-once is not cached
	if verdict == VerdictAllowOnce {
		s.cache.Resolve(cacheKey) // unblock waiters without caching
	} else {
		s.cache.Set(cacheKey, verdict)
	}

	// For always-allow, persist to TOML (still by domain, not port)
	if verdict == VerdictAlwaysAllow && s.configPath != "" {
		if err := AddDomainToConfig(s.configPath, domain); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not update config: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "Saved network allow rule for %s in %s\n", domain, s.configPath)
		}
	}

	return verdict
}
