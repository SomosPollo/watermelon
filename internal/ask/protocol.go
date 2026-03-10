package ask

// Verdict constants
const (
	VerdictAllowOnce   = "allow-once"
	VerdictAlwaysAllow = "always-allow"
	VerdictBlock       = "block"
)

// VerdictRequest is sent from the VM to the host when a connection to an
// unknown domain is intercepted.
type VerdictRequest struct {
	Domain  string `json:"domain"`
	Port    int    `json:"port"`
	Process string `json:"process"`
	IP      string `json:"ip"`
}

// VerdictResponse is sent from the host back to the VM with the user's decision.
type VerdictResponse struct {
	Verdict string `json:"verdict"`
}
