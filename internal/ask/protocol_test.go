package ask

import (
	"encoding/json"
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
