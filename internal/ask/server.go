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
	"strings"
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
	acceptRetryMinDelay    = 5 * time.Millisecond
	acceptRetryMaxDelay    = time.Second
)

// Server handles verdict requests from the VM-side nfqd daemon.
type Server struct {
	project      string
	configPath   string
	recreateCmd  string
	noticeWriter io.Writer
	cache        *Cache
	dialog       DialogFunc
	dialogMu     sync.Mutex // serializes each dialog, persistence edit, and notice
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

// ServerOption customizes verdict-server behavior.
type ServerOption func(*Server)

// WithRecreateCommand sets the exact host command shown after an always-allow
// decision is saved. The command should recreate the VM selected by the CLI so
// the saved policy can become part of its provisioned firewall.
func WithRecreateCommand(command string) ServerOption {
	return func(server *Server) {
		server.recreateCmd = command
	}
}

// WithNoticeWriter redirects always-allow persistence notices. It is primarily
// useful to callers that need to present or test those notices separately from
// the verdict dialog.
func WithNoticeWriter(writer io.Writer) ServerOption {
	return func(server *Server) {
		if writer == nil {
			writer = io.Discard
		}
		server.noticeWriter = writer
	}
}

// NewServer creates a verdict server.
// project is the project name shown in dialogs.
// configPath is the path to .watermelon.toml (for always-allow writes). Empty string disables TOML writes.
// authKey is the per-instance key shared only with the root-owned guest daemon.
// dialog is the function to show the verdict dialog. Pass nil to use the
// default host prompt. Options can supply the exact VM recreation command and
// redirect persistence notices.
func NewServer(project, configPath string, authKey AuthKey, dialog DialogFunc, options ...ServerOption) *Server {
	if dialog == nil {
		dialog = ShowDialog
	}
	server := &Server{
		project:      project,
		configPath:   configPath,
		noticeWriter: os.Stderr,
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
	for _, option := range options {
		if option != nil {
			option(server)
		}
	}
	return server
}

// Serve accepts connections on the listener and handles verdict requests.
func (s *Server) Serve(listener net.Listener) {
	var retryDelay time.Duration
	for {
		conn, err := listener.Accept()
		if err != nil {
			if conn != nil {
				_ = conn.Close()
			}
			var netErr net.Error
			if !errors.As(err, &netErr) || (!netErr.Timeout() && !netErr.Temporary()) {
				return // listener closed or failed permanently
			}
			if retryDelay == 0 {
				retryDelay = acceptRetryMinDelay
			} else {
				retryDelay = min(retryDelay*2, acceptRetryMaxDelay)
			}
			time.Sleep(retryDelay)
			continue
		}
		retryDelay = 0
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
	if domain != "" {
		// Requests are validated before reaching this path. Canonicalizing here
		// keeps the displayed, cached, and persisted host spellings identical.
		rule, _ := config.ParseNetworkRule(domain)
		domain = rule.Host
	} else {
		domain = req.IP
	}
	displayDestination := domain
	if req.IP != "" && domain != req.IP {
		// DNS attribution is deliberately prompt metadata only. nfqd does not
		// authenticate the DNS response against an outstanding query, so always
		// show the kernel-enforced literal endpoint beside the observed label.
		displayDestination = fmt.Sprintf("%s [%s]", domain, req.IP)
	}

	// The signed request carries the literal kernel destination. DNS names and
	// process names are prompt metadata only: a TCP SYN cannot prove which name
	// the workload intended, so session decisions must be scoped to the IPv4
	// endpoint the firewall can actually enforce. Older/internal callers that
	// omit IP retain the domain fallback.
	cacheDestination := req.IP
	if cacheDestination == "" {
		cacheDestination = domain
	}
	cacheKey := fmt.Sprintf("%s:%d", cacheDestination, req.Port)

	// Check cache first
	if v, ok := s.cache.Get(cacheKey); ok {
		return v
	}

	// Check if another goroutine is already showing a dialog for this endpoint.
	if ch := s.cache.MarkPending(cacheKey); ch != nil {
		<-ch // wait for the other dialog to complete
		if v, ok := s.cache.Get(cacheKey); ok {
			return v
		}
		// Not cached (e.g. allow-once) — re-enter to prompt again
		return s.getVerdict(req)
	}

	// We're the first — handle the full interactive decision one at a time. Keep
	// the lock through persistence and its notice so a second prompt cannot be
	// overwritten by the first decision's delayed output.
	s.dialogMu.Lock()
	defer s.dialogMu.Unlock()
	verdict := s.dialog(req.Process, displayDestination, req.Port, s.project)
	if !ValidVerdict(verdict) {
		verdict = VerdictBlock
	}

	// Cache block and always-allow for the session; allow-once is not cached
	if verdict == VerdictAllowOnce {
		s.cache.Resolve(cacheKey) // unblock waiters without caching
	} else {
		s.cache.Set(cacheKey, verdict)
	}

	// Always Allow deliberately persists a global bare-host rule. The prompt
	// labels the observed process as informational and states that the saved
	// rule has no process, protocol, or port scope, rather than implying that
	// the displayed process or TCP destination port will be retained.
	if verdict == VerdictAlwaysAllow && s.configPath != "" {
		added, err := AddDomainToConfig(s.configPath, domain)
		var notice strings.Builder
		if err != nil {
			fmt.Fprintf(&notice, "Allowed TCP destination %s:%d for all processes in the current VM runtime, but the global host-only rule was not saved: %v\n", displayDestination, req.Port, err)
		} else {
			action := "Saved"
			if !added {
				action = "Found existing"
			}
			fmt.Fprintf(&notice, "Allowed TCP destination %s:%d for all processes in the current VM runtime.\n", displayDestination, req.Port)
			fmt.Fprintf(&notice, "%s global host-only rule %q with no process, protocol, or port scope in %s; managed DNS remains enforced.\n", action, domain, s.configPath)
			if s.recreateCmd != "" {
				fmt.Fprintln(&notice, "After this Watermelon session finishes, run the following command from the project root to apply the saved rule to future VM sessions (recreation removes VM-local state):")
				fmt.Fprintf(&notice, "  %s\n", s.recreateCmd)
			} else {
				fmt.Fprintln(&notice, "Recreate the VM after this Watermelon session finishes to apply the saved rule to future VM sessions (recreation removes VM-local state).")
			}
		}
		writeDecisionNotice(s.noticeWriter, notice.String())
	}

	return verdict
}

func writeDecisionNotice(writer io.Writer, notice string) {
	// Interactive guest commands may leave their terminal in a raw state with
	// output post-processing disabled. Use explicit CRLF for a terminal so this
	// host-side notice still starts each line in the first column. Redirected
	// diagnostics retain ordinary LF bytes.
	if file, ok := writer.(*os.File); ok && fileIsTerminal(file) {
		notice = strings.ReplaceAll(notice, "\n", "\r\n")
	}
	_, _ = io.WriteString(writer, notice)
}
