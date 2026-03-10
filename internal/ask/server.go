package ask

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"
)

// Server handles verdict requests from the VM-side nfqd daemon.
type Server struct {
	project    string
	configPath string
	cache      *Cache
	dialog     DialogFunc
	dialogMu   sync.Mutex // ensures one dialog at a time
}

// NewServer creates a verdict server.
// project is the project name shown in dialogs.
// configPath is the path to .watermelon.toml (for always-allow writes). Empty string disables TOML writes.
// dialog is the function to show the verdict dialog. Pass nil to use the real macOS dialog.
func NewServer(project, configPath string, dialog DialogFunc) *Server {
	if dialog == nil {
		dialog = ShowDialog
	}
	return &Server{
		project:    project,
		configPath: configPath,
		cache:      NewCache(),
		dialog:     dialog,
	}
}

// Serve accepts connections on the listener and handles verdict requests.
func (s *Server) Serve(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return // listener closed
		}
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()

	var req VerdictRequest
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		return
	}

	verdict := s.getVerdict(req)

	resp := VerdictResponse{Verdict: verdict}
	json.NewEncoder(conn).Encode(resp)
}

func (s *Server) getVerdict(req VerdictRequest) string {
	domain := req.Domain
	if domain == "" {
		domain = req.IP
	}

	// Check cache first
	if v, ok := s.cache.Get(domain); ok {
		return v
	}

	// Check if another goroutine is already showing a dialog for this domain
	if ch := s.cache.MarkPending(domain); ch != nil {
		<-ch // wait for the other dialog to complete
		if v, ok := s.cache.Get(domain); ok {
			return v
		}
		// Not cached (e.g. allow-once) — re-enter to prompt again
		return s.getVerdict(req)
	}

	// We're the first — show dialog (one at a time)
	s.dialogMu.Lock()
	verdict := s.dialog(req.Process, domain, req.Port, s.project)
	s.dialogMu.Unlock()

	// Cache block and always-allow for the session; allow-once is not cached
	if verdict == VerdictAllowOnce {
		s.cache.Resolve(domain) // unblock waiters without caching
	} else {
		s.cache.Set(domain, verdict)
	}

	// For always-allow, persist to TOML
	if verdict == VerdictAlwaysAllow && s.configPath != "" {
		if err := AddDomainToConfig(s.configPath, domain); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not update config: %v\n", err)
		}
	}

	return verdict
}
