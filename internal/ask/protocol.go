package ask

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash"
	"strings"
)

// Verdict constants
const (
	VerdictAllowOnce   = "allow-once"
	VerdictAlwaysAllow = "always-allow"
	VerdictBlock       = "block"

	AuthKeyBytes = 32
	nonceBytes   = 32
)

const (
	requestMACContext  = "watermelon-verdict-request-v1"
	responseMACContext = "watermelon-verdict-response-v1"
)

// AuthKey is the per-instance secret shared by the host prompt controller and
// the root-owned nfqd daemon. It is never sent over the verdict connection.
type AuthKey [AuthKeyBytes]byte

// NewAuthKey returns a cryptographically random per-instance authentication
// key.
func NewAuthKey() (AuthKey, error) {
	var key AuthKey
	if _, err := rand.Read(key[:]); err != nil {
		return AuthKey{}, err
	}
	return key, nil
}

// ParseAuthKey parses the canonical lowercase hexadecimal representation used
// by the private host identity state and the root-only guest key file.
func ParseAuthKey(encoded string) (AuthKey, error) {
	var key AuthKey
	if len(encoded) != AuthKeyBytes*2 || encoded != strings.ToLower(encoded) {
		return AuthKey{}, errors.New("verdict authentication key must be 64 lowercase hexadecimal characters")
	}
	decoded, err := hex.DecodeString(encoded)
	if err != nil || len(decoded) != len(key) {
		return AuthKey{}, errors.New("verdict authentication key must be 64 lowercase hexadecimal characters")
	}
	copy(key[:], decoded)
	return key, nil
}

// Hex returns the canonical representation suitable for the private key
// files. Callers must not log this value.
func (k AuthKey) Hex() string {
	return hex.EncodeToString(k[:])
}

// VerdictRequest is sent from the VM to the host when a connection to an
// unknown domain is intercepted.
type VerdictRequest struct {
	Domain  string `json:"domain"`
	Port    int    `json:"port"`
	Process string `json:"process"`
	IP      string `json:"ip"`
	Nonce   string `json:"nonce"`
	MAC     string `json:"mac"`
}

// VerdictResponse is sent from the host back to the VM with the user's decision.
type VerdictResponse struct {
	Verdict string `json:"verdict"`
	Nonce   string `json:"nonce"`
	MAC     string `json:"mac"`
}

// AuthenticateRequest adds a fresh random nonce and a request HMAC. A new
// nonce is required for every connection so a response cannot be reused for a
// different intercepted packet.
func AuthenticateRequest(key AuthKey, req *VerdictRequest) error {
	if req == nil {
		return errors.New("nil verdict request")
	}
	nonce := make([]byte, nonceBytes)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	req.Nonce = hex.EncodeToString(nonce)
	req.MAC = hex.EncodeToString(requestMAC(key, *req, nonce))
	return nil
}

// VerifyRequest authenticates all decision-relevant request fields using a
// constant-time HMAC comparison.
func VerifyRequest(key AuthKey, req VerdictRequest) bool {
	nonce, ok := decodeFixedLowerHex(req.Nonce, nonceBytes)
	if !ok {
		return false
	}
	provided, ok := decodeFixedLowerHex(req.MAC, sha256.Size)
	if !ok {
		return false
	}
	expected := requestMAC(key, req, nonce)
	return hmac.Equal(provided, expected)
}

// AuthenticateResponse binds a verdict to the authenticated request and its
// nonce. Invalid verdict values are never signed.
func AuthenticateResponse(key AuthKey, req VerdictRequest, verdict string) (VerdictResponse, error) {
	if !ValidVerdict(verdict) {
		return VerdictResponse{}, errors.New("invalid verdict")
	}
	nonce, ok := decodeFixedLowerHex(req.Nonce, nonceBytes)
	if !ok {
		return VerdictResponse{}, errors.New("invalid request nonce")
	}
	requestTag, ok := decodeFixedLowerHex(req.MAC, sha256.Size)
	if !ok {
		return VerdictResponse{}, errors.New("invalid request MAC")
	}
	resp := VerdictResponse{Verdict: verdict, Nonce: req.Nonce}
	resp.MAC = hex.EncodeToString(responseMAC(key, requestTag, nonce, resp))
	return resp, nil
}

// VerifyResponse authenticates the response, requires the exact request nonce,
// and rejects every value outside the closed verdict enum.
func VerifyResponse(key AuthKey, req VerdictRequest, resp VerdictResponse) bool {
	if !ValidVerdict(resp.Verdict) || resp.Nonce != req.Nonce {
		return false
	}
	nonce, ok := decodeFixedLowerHex(resp.Nonce, nonceBytes)
	if !ok {
		return false
	}
	requestTag, ok := decodeFixedLowerHex(req.MAC, sha256.Size)
	if !ok {
		return false
	}
	provided, ok := decodeFixedLowerHex(resp.MAC, sha256.Size)
	if !ok {
		return false
	}
	expected := responseMAC(key, requestTag, nonce, resp)
	return hmac.Equal(provided, expected)
}

// ValidVerdict reports whether verdict is one of the only three protocol
// values understood by nfqd.
func ValidVerdict(verdict string) bool {
	switch verdict {
	case VerdictAllowOnce, VerdictAlwaysAllow, VerdictBlock:
		return true
	default:
		return false
	}
}

func requestMAC(key AuthKey, req VerdictRequest, nonce []byte) []byte {
	mac := hmac.New(sha256.New, key[:])
	writeMACField(mac, []byte(requestMACContext))
	writeMACField(mac, []byte(req.Domain))
	var port [8]byte
	binary.BigEndian.PutUint64(port[:], uint64(int64(req.Port)))
	writeMACField(mac, port[:])
	writeMACField(mac, []byte(req.Process))
	writeMACField(mac, []byte(req.IP))
	writeMACField(mac, nonce)
	return mac.Sum(nil)
}

func responseMAC(key AuthKey, requestTag, nonce []byte, resp VerdictResponse) []byte {
	mac := hmac.New(sha256.New, key[:])
	writeMACField(mac, []byte(responseMACContext))
	writeMACField(mac, requestTag)
	writeMACField(mac, nonce)
	writeMACField(mac, []byte(resp.Verdict))
	return mac.Sum(nil)
}

func writeMACField(mac hash.Hash, value []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = mac.Write(size[:])
	_, _ = mac.Write(value)
}

func decodeFixedLowerHex(encoded string, size int) ([]byte, bool) {
	if len(encoded) != size*2 || encoded != strings.ToLower(encoded) {
		return nil, false
	}
	decoded, err := hex.DecodeString(encoded)
	return decoded, err == nil && len(decoded) == size
}
