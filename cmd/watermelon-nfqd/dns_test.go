package main

import (
	"encoding/binary"
	"testing"
)

// buildDNSPacket constructs a minimal IP+UDP+DNS response where "example.com" -> 1.2.3.4
func buildDNSPacket(t *testing.T) []byte {
	t.Helper()
	ip := []byte{
		0x45, 0x00, 0x00, 0x00, // version/IHL, DSCP, total length
		0x00, 0x00, 0x00, 0x00, // identification, flags, fragment offset
		0x40, 0x11, 0x00, 0x00, // TTL, protocol (UDP), header checksum
		0x08, 0x08, 0x08, 0x08, // src IP: 8.8.8.8
		0x0a, 0x00, 0x00, 0x01, // dst IP: 10.0.0.1
	}
	udp := []byte{
		0x00, 0x35, 0xc0, 0x00, // src port 53, dst port 49152
		0x00, 0x00, 0x00, 0x00, // length, checksum
	}
	dns := []byte{
		0x00, 0x01, // Transaction ID
		0x81, 0x80, // Flags: standard response, no error
		0x00, 0x01, // QDCOUNT: 1
		0x00, 0x01, // ANCOUNT: 1
		0x00, 0x00, // NSCOUNT: 0
		0x00, 0x00, // ARCOUNT: 0
		// Question: example.com A IN
		0x07, 'e', 'x', 'a', 'm', 'p', 'l', 'e',
		0x03, 'c', 'o', 'm', 0x00,
		0x00, 0x01, // QTYPE: A
		0x00, 0x01, // QCLASS: IN
		// Answer: example.com -> 1.2.3.4
		0xc0, 0x0c, // Name: pointer to offset 12 (example.com)
		0x00, 0x01, // TYPE: A
		0x00, 0x01, // CLASS: IN
		0x00, 0x00, 0x00, 0x3c, // TTL: 60
		0x00, 0x04, // RDLENGTH: 4
		0x01, 0x02, 0x03, 0x04, // RDATA: 1.2.3.4
	}
	var pkt []byte
	pkt = append(pkt, ip...)
	pkt = append(pkt, udp...)
	pkt = append(pkt, dns...)
	return pkt
}

func TestParseDNSResponse(t *testing.T) {
	pkt := buildDNSPacket(t)
	result := parseDNSResponse(pkt)

	if len(result) != 1 {
		t.Fatalf("expected 1 mapping, got %d: %v", len(result), result)
	}
	domain, ok := result["1.2.3.4"]
	if !ok {
		t.Fatal("expected mapping for 1.2.3.4")
	}
	if domain != "example.com" {
		t.Errorf("expected domain example.com, got %q", domain)
	}
}

func TestParseDNSResponseNoAnswers(t *testing.T) {
	pkt := buildDNSPacket(t)
	// Patch ANCOUNT to 0. DNS header starts at IP(20) + UDP(8) = 28.
	// ANCOUNT is at DNS offset 6-7, so byte 34-35 in the packet.
	pkt[34] = 0x00
	pkt[35] = 0x00

	result := parseDNSResponse(pkt)
	if len(result) != 0 {
		t.Errorf("expected 0 mappings, got %d: %v", len(result), result)
	}
}

func TestParseDNSResponseTooShort(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
	}{
		{"empty", []byte{}},
		{"one byte", []byte{0x45}},
		{"ip only", []byte{
			0x45, 0x00, 0x00, 0x00,
			0x00, 0x00, 0x00, 0x00,
			0x40, 0x11, 0x00, 0x00,
			0x08, 0x08, 0x08, 0x08,
			0x0a, 0x00, 0x00, 0x01,
		}},
		{"ip+udp no dns", []byte{
			0x45, 0x00, 0x00, 0x00,
			0x00, 0x00, 0x00, 0x00,
			0x40, 0x11, 0x00, 0x00,
			0x08, 0x08, 0x08, 0x08,
			0x0a, 0x00, 0x00, 0x01,
			0x00, 0x35, 0xc0, 0x00,
			0x00, 0x00, 0x00, 0x00,
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Should not panic on short payloads
			result := parseDNSResponse(tc.payload)
			if len(result) != 0 {
				t.Errorf("expected 0 mappings, got %d", len(result))
			}
		})
	}
}

func TestParseDNSResponseMultipleAnswers(t *testing.T) {
	pkt := buildDNSPacket(t)

	// Add a second A record answer: example.com -> 5.6.7.8
	secondAnswer := []byte{
		0xc0, 0x0c, // Name: pointer to offset 12 (example.com)
		0x00, 0x01, // TYPE: A
		0x00, 0x01, // CLASS: IN
		0x00, 0x00, 0x00, 0x3c, // TTL: 60
		0x00, 0x04, // RDLENGTH: 4
		0x05, 0x06, 0x07, 0x08, // RDATA: 5.6.7.8
	}
	pkt = append(pkt, secondAnswer...)

	// Patch ANCOUNT to 2. DNS header at offset 28; ANCOUNT at DNS offset 6-7.
	pkt[34] = 0x00
	pkt[35] = 0x02

	result := parseDNSResponse(pkt)
	if len(result) != 2 {
		t.Fatalf("expected 2 mappings, got %d: %v", len(result), result)
	}

	if d, ok := result["1.2.3.4"]; !ok || d != "example.com" {
		t.Errorf("expected 1.2.3.4 -> example.com, got %q (ok=%v)", d, ok)
	}
	if d, ok := result["5.6.7.8"]; !ok || d != "example.com" {
		t.Errorf("expected 5.6.7.8 -> example.com, got %q (ok=%v)", d, ok)
	}
}

func TestSkipDNSName(t *testing.T) {
	// Label sequence: 7"example" 3"com" 0
	dns := []byte{
		0x07, 'e', 'x', 'a', 'm', 'p', 'l', 'e',
		0x03, 'c', 'o', 'm', 0x00,
	}
	offset := skipDNSName(dns, 0)
	// Should point past the terminating null byte
	if offset != 13 {
		t.Errorf("expected offset 13, got %d", offset)
	}
}

func TestSkipDNSNamePointer(t *testing.T) {
	// Pointer: 0xC0 0x0C
	dns := []byte{0xc0, 0x0c}
	offset := skipDNSName(dns, 0)
	// Pointer is 2 bytes
	if offset != 2 {
		t.Errorf("expected offset 2, got %d", offset)
	}
}

func TestSkipDNSNameTruncated(t *testing.T) {
	// Label claiming length 7 but not enough data
	dns := []byte{0x07, 'e', 'x'}
	offset := skipDNSName(dns, 0)
	// Should return -1 because we run past the end
	if offset != -1 {
		t.Errorf("expected offset -1 for truncated name, got %d", offset)
	}
}

func TestReadDNSName(t *testing.T) {
	dns := []byte{
		0x07, 'e', 'x', 'a', 'm', 'p', 'l', 'e',
		0x03, 'c', 'o', 'm', 0x00,
	}
	name := readDNSName(dns, 0)
	if name != "example.com" {
		t.Errorf("expected example.com, got %q", name)
	}
}

func TestReadDNSNameWithPointer(t *testing.T) {
	// DNS buffer with a name at offset 0 and a pointer at offset 13
	dns := []byte{
		// offset 0: "example.com"
		0x07, 'e', 'x', 'a', 'm', 'p', 'l', 'e',
		0x03, 'c', 'o', 'm', 0x00,
		// offset 13: pointer to offset 0
		0xc0, 0x00,
	}
	name := readDNSName(dns, 13)
	if name != "example.com" {
		t.Errorf("expected example.com, got %q", name)
	}
}

func TestReadDNSNameEmpty(t *testing.T) {
	// Null byte (root label) means empty name
	dns := []byte{0x00}
	name := readDNSName(dns, 0)
	if name != "" {
		t.Errorf("expected empty string, got %q", name)
	}
}

func TestReadDNSNameCircularPointer(t *testing.T) {
	// Self-referencing pointer: offset 0 points to offset 0
	dns := make([]byte, 2)
	binary.BigEndian.PutUint16(dns, 0xC000) // pointer to offset 0
	name := readDNSName(dns, 0)
	// Should detect the cycle and return empty
	if name != "" {
		t.Errorf("expected empty string for circular pointer, got %q", name)
	}
}
