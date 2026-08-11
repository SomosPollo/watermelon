package ask

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestVerdictRequestJSON(t *testing.T) {
	req := VerdictRequest{
		Domain:  "evil.com",
		Port:    443,
		Process: "npm",
		IP:      "93.184.216.34",
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got VerdictRequest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got != req {
		t.Errorf("roundtrip mismatch: got %+v, want %+v", got, req)
	}
}

func TestVerdictResponseJSON(t *testing.T) {
	resp := VerdictResponse{Verdict: VerdictAlwaysAllow}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got VerdictResponse
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Verdict != VerdictAlwaysAllow {
		t.Errorf("got verdict %q, want %q", got.Verdict, VerdictAlwaysAllow)
	}
}

func TestVerdictConstants(t *testing.T) {
	if VerdictAllowOnce != "allow-once" {
		t.Error("VerdictAllowOnce should be 'allow-once'")
	}
	if VerdictAlwaysAllow != "always-allow" {
		t.Error("VerdictAlwaysAllow should be 'always-allow'")
	}
	if VerdictBlock != "block" {
		t.Error("VerdictBlock should be 'block'")
	}
}

func TestVerdictProtocolAuthenticatesRequestAndResponse(t *testing.T) {
	key, err := ParseAuthKey("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	wrongKey, err := ParseAuthKey("1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	req := VerdictRequest{Domain: "example.com", Port: 443, Process: "npm", IP: "93.184.216.34"}
	if err := AuthenticateRequest(key, &req); err != nil {
		t.Fatal(err)
	}
	if !VerifyRequest(key, req) {
		t.Fatal("authenticated request did not verify")
	}
	if VerifyRequest(wrongKey, req) {
		t.Fatal("request verified with the wrong per-instance key")
	}
	if len(req.Nonce) != nonceBytes*2 || len(req.MAC) != 64 {
		t.Fatalf("request nonce/MAC lengths = %d/%d", len(req.Nonce), len(req.MAC))
	}

	resp, err := AuthenticateResponse(key, req, VerdictAllowOnce)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyResponse(key, req, resp) {
		t.Fatal("authenticated response did not verify")
	}
	if VerifyResponse(wrongKey, req, resp) {
		t.Fatal("response verified with the wrong per-instance key")
	}

	tamperedReq := req
	tamperedReq.Domain = "attacker.example"
	if VerifyRequest(key, tamperedReq) {
		t.Fatal("request HMAC did not bind the domain")
	}
	tamperedReq = req
	tamperedReq.Port = 80
	if VerifyRequest(key, tamperedReq) {
		t.Fatal("request HMAC did not bind the port")
	}
	tamperedResp := resp
	tamperedResp.Verdict = VerdictAlwaysAllow
	if VerifyResponse(key, req, tamperedResp) {
		t.Fatal("response HMAC did not bind the verdict")
	}
	tamperedResp = resp
	tamperedResp.Nonce = strings.Repeat("0", nonceBytes*2)
	if VerifyResponse(key, req, tamperedResp) {
		t.Fatal("response accepted a different request nonce")
	}
}

func TestAuthenticateRequestUsesFreshNonceAndDoesNotTransmitKey(t *testing.T) {
	key, err := NewAuthKey()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseAuthKey(key.Hex())
	if err != nil || parsed != key {
		t.Fatalf("key roundtrip failed: parsed=%x err=%v", parsed, err)
	}
	first := VerdictRequest{Domain: "example.com", Port: 443}
	second := first
	if err := AuthenticateRequest(key, &first); err != nil {
		t.Fatal(err)
	}
	if err := AuthenticateRequest(key, &second); err != nil {
		t.Fatal(err)
	}
	if first.Nonce == second.Nonce || first.MAC == second.MAC {
		t.Fatal("separate requests reused nonce-authentication material")
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), key.Hex()) || strings.Contains(string(encoded), "token") || strings.Contains(string(encoded), "key") {
		t.Fatalf("request exposed shared authentication key: %s", encoded)
	}
}

func TestVerdictProtocolRejectsInvalidFormatsAndVerdicts(t *testing.T) {
	if _, err := ParseAuthKey(""); err == nil {
		t.Fatal("empty authentication key was accepted")
	}
	if _, err := ParseAuthKey(strings.Repeat("A", AuthKeyBytes*2)); err == nil {
		t.Fatal("non-canonical authentication key was accepted")
	}
	key, err := NewAuthKey()
	if err != nil {
		t.Fatal(err)
	}
	req := VerdictRequest{Domain: "example.com", Port: 443}
	if err := AuthenticateRequest(key, &req); err != nil {
		t.Fatal(err)
	}
	if _, err := AuthenticateResponse(key, req, "allow"); err == nil {
		t.Fatal("invalid verdict was signed")
	}
	for _, verdict := range []string{"", "allow", "ALLOW-ONCE", "block ", "always_allow"} {
		if ValidVerdict(verdict) {
			t.Errorf("invalid verdict %q was accepted", verdict)
		}
	}
}
