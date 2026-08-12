package main

import (
	"encoding/binary"
	"reflect"
	"testing"
	"time"
)

// buildDNSPacket constructs a minimal IP+UDP+DNS response where "example.com" -> 1.2.3.4
func buildDNSPacket() []byte {
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
	setDNSPacketLengths(pkt)
	return pkt
}

func setDNSPacketLengths(packet []byte) {
	binary.BigEndian.PutUint16(packet[2:4], uint16(len(packet)))
	ihl := int(packet[0]&0x0f) * 4
	binary.BigEndian.PutUint16(packet[ihl+4:ihl+6], uint16(len(packet)-ihl))
}

type testDNSAnswer struct {
	name   string
	qtype  uint16
	qclass uint16
	ttl    uint32
	rdata  []byte
}

func encodeDNSName(name string) []byte {
	var encoded []byte
	start := 0
	for i := 0; i <= len(name); i++ {
		if i != len(name) && name[i] != '.' {
			continue
		}
		encoded = append(encoded, byte(i-start))
		encoded = append(encoded, name[start:i]...)
		start = i + 1
	}
	return append(encoded, 0)
}

func buildDNSResponse(question string, questionType, questionClass uint16, answers []testDNSAnswer) []byte {
	dns := make([]byte, 12)
	binary.BigEndian.PutUint16(dns[0:2], 1)
	binary.BigEndian.PutUint16(dns[2:4], 0x8180)
	binary.BigEndian.PutUint16(dns[4:6], 1)
	binary.BigEndian.PutUint16(dns[6:8], uint16(len(answers)))
	dns = append(dns, encodeDNSName(question)...)
	dns = binary.BigEndian.AppendUint16(dns, questionType)
	dns = binary.BigEndian.AppendUint16(dns, questionClass)
	for _, answer := range answers {
		dns = append(dns, encodeDNSName(answer.name)...)
		dns = binary.BigEndian.AppendUint16(dns, answer.qtype)
		dns = binary.BigEndian.AppendUint16(dns, answer.qclass)
		dns = binary.BigEndian.AppendUint32(dns, answer.ttl)
		dns = binary.BigEndian.AppendUint16(dns, uint16(len(answer.rdata)))
		dns = append(dns, answer.rdata...)
	}

	packet := make([]byte, 28, 28+len(dns))
	packet[0] = 0x45
	packet[8] = 64
	packet[9] = 17
	copy(packet[12:16], []byte{8, 8, 8, 8})
	copy(packet[16:20], []byte{10, 0, 0, 1})
	binary.BigEndian.PutUint16(packet[20:22], 53)
	binary.BigEndian.PutUint16(packet[22:24], 49152)
	packet = append(packet, dns...)
	setDNSPacketLengths(packet)
	return packet
}

func aAnswer(name string, ttl uint32, ip [4]byte) testDNSAnswer {
	return testDNSAnswer{name: name, qtype: dnsTypeA, qclass: dnsClassIN, ttl: ttl, rdata: ip[:]}
}

func cnameAnswer(name, target string, ttl uint32) testDNSAnswer {
	return testDNSAnswer{name: name, qtype: dnsTypeCNAME, qclass: dnsClassIN, ttl: ttl, rdata: encodeDNSName(target)}
}

func TestParseDNSResponse(t *testing.T) {
	pkt := buildDNSPacket()
	result := parseDNSResponse(pkt)

	if len(result) != 1 {
		t.Fatalf("expected 1 mapping, got %d: %v", len(result), result)
	}
	want := dnsMapping{IP: "1.2.3.4", Domain: "example.com", TTL: time.Minute}
	if result[0] != want {
		t.Errorf("mapping = %#v, want %#v", result[0], want)
	}
}

func TestParseDNSResponseNoAnswers(t *testing.T) {
	pkt := buildDNSPacket()
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

func TestParseDNSResponseRejectsIHLBeyondCapture(t *testing.T) {
	packet := buildDNSPacket()
	packet[0] = 0x4f // claims a 60-byte IPv4 header
	packet = packet[:59]
	if got := parseDNSResponse(packet); len(got) != 0 {
		t.Fatalf("parseDNSResponse() = %#v, want rejection when IHL exceeds captured bytes", got)
	}
}

func TestParseDNSResponseMultipleAnswers(t *testing.T) {
	pkt := buildDNSPacket()

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
	setDNSPacketLengths(pkt)

	// Patch ANCOUNT to 2. DNS header at offset 28; ANCOUNT at DNS offset 6-7.
	pkt[34] = 0x00
	pkt[35] = 0x02

	result := parseDNSResponse(pkt)
	if len(result) != 2 {
		t.Fatalf("expected 2 mappings, got %d: %v", len(result), result)
	}

	want := []dnsMapping{
		{IP: "1.2.3.4", Domain: "example.com", TTL: time.Minute},
		{IP: "5.6.7.8", Domain: "example.com", TTL: time.Minute},
	}
	if !reflect.DeepEqual(result, want) {
		t.Errorf("mappings = %#v, want %#v", result, want)
	}
}

func TestParseDNSResponseFollowsQuestionCNAMEChain(t *testing.T) {
	packet := buildDNSResponse("Packages.Example", dnsTypeA, dnsClassIN, []testDNSAnswer{
		cnameAnswer("packages.example", "edge.example", 45),
		cnameAnswer("edge.example", "terminal.example", 20),
		aAnswer("terminal.example", 60, [4]byte{192, 0, 2, 40}),
	})

	want := []dnsMapping{{IP: "192.0.2.40", Domain: "packages.example", TTL: 20 * time.Second}}
	if got := parseDNSResponse(packet); !reflect.DeepEqual(got, want) {
		t.Fatalf("parseDNSResponse() = %#v, want CNAME-attributed mapping %#v", got, want)
	}
}

func TestParseDNSResponseIgnoresUnrelatedAnswers(t *testing.T) {
	packet := buildDNSResponse("wanted.example", dnsTypeA, dnsClassIN, []testDNSAnswer{
		aAnswer("attacker.example", 60, [4]byte{203, 0, 113, 66}),
		cnameAnswer("unrelated.example", "attacker.example", 60),
		aAnswer("wanted.example", 30, [4]byte{192, 0, 2, 50}),
	})

	want := []dnsMapping{{IP: "192.0.2.50", Domain: "wanted.example", TTL: 30 * time.Second}}
	if got := parseDNSResponse(packet); !reflect.DeepEqual(got, want) {
		t.Fatalf("parseDNSResponse() = %#v, want only queried-name mapping %#v", got, want)
	}
}

func TestParseDNSResponseRequiresOneAInQuestion(t *testing.T) {
	tests := []struct {
		name          string
		questionType  uint16
		questionClass uint16
		questionCount uint16
	}{
		{name: "AAAA question", questionType: 28, questionClass: dnsClassIN, questionCount: 1},
		{name: "non-IN question", questionType: dnsTypeA, questionClass: 3, questionCount: 1},
		{name: "zero questions", questionType: dnsTypeA, questionClass: dnsClassIN, questionCount: 0},
		{name: "two questions", questionType: dnsTypeA, questionClass: dnsClassIN, questionCount: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			packet := buildDNSResponse("example.com", test.questionType, test.questionClass, []testDNSAnswer{
				aAnswer("example.com", 60, [4]byte{1, 2, 3, 4}),
			})
			binary.BigEndian.PutUint16(packet[32:34], test.questionCount)
			if got := parseDNSResponse(packet); len(got) != 0 {
				t.Fatalf("parseDNSResponse() = %#v, want no mappings", got)
			}
		})
	}
}

func TestParseDNSResponseRejectsInvalidResponseModes(t *testing.T) {
	tests := []struct {
		name string
		flag uint16
	}{
		{"truncated", dnsFlagTruncated},
		{"non-standard opcode", 1 << 11},
		{"reserved Z bit", dnsReservedZ},
		{"server failure", 2},
		{"name error", 3},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			packet := buildDNSPacket()
			flags := binary.BigEndian.Uint16(packet[30:32]) | test.flag
			binary.BigEndian.PutUint16(packet[30:32], flags)
			if got := parseDNSResponse(packet); len(got) != 0 {
				t.Fatalf("parseDNSResponse() = %#v, want rejected response mode", got)
			}
		})
	}
}

func TestParseDNSResponseHandlesLargeResponseBeyondLegacyCopyLimit(t *testing.T) {
	packet := buildDNSResponse("example.com", dnsTypeA, dnsClassIN, []testDNSAnswer{
		{name: "unrelated.example", qtype: 16, qclass: dnsClassIN, ttl: 60, rdata: make([]byte, 700)},
		aAnswer("example.com", 60, [4]byte{1, 2, 3, 4}),
	})
	if len(packet) <= 512 {
		t.Fatalf("test packet is only %d bytes; want it beyond the legacy copy limit", len(packet))
	}
	want := []dnsMapping{{IP: "1.2.3.4", Domain: "example.com", TTL: time.Minute}}
	if got := parseDNSResponse(packet); !reflect.DeepEqual(got, want) {
		t.Fatalf("parseDNSResponse(%d-byte packet) = %#v, want %#v", len(packet), got, want)
	}
}

func TestParseDNSResponseSupportsIPv4OptionsAtIHLBoundary(t *testing.T) {
	original := buildDNSPacket()
	packet := make([]byte, 0, len(original)+40)
	packet = append(packet, original[:20]...)
	packet = append(packet, make([]byte, 40)...)
	packet = append(packet, original[20:]...)
	packet[0] = 0x4f // maximum 60-byte IPv4 header
	setDNSPacketLengths(packet)

	want := []dnsMapping{{IP: "1.2.3.4", Domain: "example.com", TTL: time.Minute}}
	if got := parseDNSResponse(packet); !reflect.DeepEqual(got, want) {
		t.Fatalf("parseDNSResponse(max-IHL packet) = %#v, want %#v", got, want)
	}
}

func TestDNSQueueCopiesFullIPv4Packet(t *testing.T) {
	if dnsQueueMaxPacketLen != 65535 {
		t.Fatalf("dnsQueueMaxPacketLen = %d, want full IPv4 packet length 65535", dnsQueueMaxPacketLen)
	}

	packet := buildDNSPacket()
	additionalLength := int(dnsQueueMaxPacketLen) - len(packet)
	rdataLength := additionalLength - 11 // root name + fixed RR fields
	additional := []byte{
		0,     // root owner name
		0, 10, // NULL record
		0, 1, // IN class
		0, 0, 0, 0, // TTL
		byte(rdataLength >> 8), byte(rdataLength),
	}
	additional = append(additional, make([]byte, rdataLength)...)
	packet = append(packet, additional...)
	binary.BigEndian.PutUint16(packet[38:40], 1) // ARCOUNT
	setDNSPacketLengths(packet)
	if len(packet) != int(dnsQueueMaxPacketLen) {
		t.Fatalf("boundary packet length = %d, want %d", len(packet), dnsQueueMaxPacketLen)
	}
	if got := parseDNSResponse(packet); len(got) != 1 || got[0].IP != "1.2.3.4" {
		t.Fatalf("parseDNSResponse(max IPv4 packet) = %#v, want queried A mapping", got)
	}
}

func TestParseDNSResponseRejectsMalformedPacketHeaders(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]byte)
	}{
		{"not IPv4", func(packet []byte) { packet[0] = 0x65 }},
		{"IHL below minimum", func(packet []byte) { packet[0] = 0x44 }},
		{"not UDP", func(packet []byte) { packet[9] = 6 }},
		{"fragmented", func(packet []byte) { packet[6] = 0x20 }},
		{"wrong UDP source port", func(packet []byte) { binary.BigEndian.PutUint16(packet[20:22], 54) }},
		{"zero IP length", func(packet []byte) { binary.BigEndian.PutUint16(packet[2:4], 0) }},
		{"truncated IP length", func(packet []byte) { binary.BigEndian.PutUint16(packet[2:4], uint16(len(packet)+1)) }},
		{"short UDP length", func(packet []byte) { binary.BigEndian.PutUint16(packet[24:26], 8) }},
		{"truncated UDP length", func(packet []byte) { binary.BigEndian.PutUint16(packet[24:26], uint16(len(packet))) }},
		{"not a response", func(packet []byte) { packet[30] &^= 0x80 }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			packet := buildDNSPacket()
			test.mutate(packet)
			if got := parseDNSResponse(packet); len(got) != 0 {
				t.Fatalf("parseDNSResponse() = %#v, want no mappings", got)
			}
		})
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
	// A pointer at offset 13 refers back to the name at offset 0.
	dns := []byte{
		0x07, 'e', 'x', 'a', 'm', 'p', 'l', 'e',
		0x03, 'c', 'o', 'm', 0x00,
		0xc0, 0x00,
	}
	offset := skipDNSName(dns, 13)
	// Pointer is 2 bytes
	if offset != 15 {
		t.Errorf("expected offset 15, got %d", offset)
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

func TestReadDNSNameRejectsForwardPointer(t *testing.T) {
	dns := []byte{0xc0, 0x02, 0x00}
	if name := readDNSName(dns, 0); name != "" {
		t.Fatalf("forward pointer returned %q, want rejection", name)
	}
	if offset := skipDNSName(dns, 0); offset != -1 {
		t.Fatalf("skipDNSName(forward pointer) = %d, want -1", offset)
	}
}

func TestReadDNSNameCapsCompressionPointerTraversal(t *testing.T) {
	const pointers = maxDNSCompressionPointers + 1
	dns := make([]byte, pointers*2+2)
	dns[0] = 0 // root label at the end of the pointer chain
	for i := 1; i <= pointers; i++ {
		offset := i * 2
		target := offset - 2
		binary.BigEndian.PutUint16(dns[offset:offset+2], uint16(0xc000|target))
	}
	if name := readDNSName(dns, pointers*2); name != "" {
		t.Fatalf("overlong compression chain returned %q, want rejection", name)
	}
}

func TestParseDNSResponseRejectsTruncatedDeclaredAdditionalRecord(t *testing.T) {
	packet := buildDNSPacket()
	// The DNS header starts after the 20-byte IPv4 and 8-byte UDP headers.
	binary.BigEndian.PutUint16(packet[38:40], 1) // DNS ARCOUNT at packet offset 28+10
	if got := parseDNSResponse(packet); len(got) != 0 {
		t.Fatalf("parseDNSResponse() = %#v, want truncated additional section rejected", got)
	}
}

func TestParseDNSResponseValidatesCompressedIgnoredRecordOwners(t *testing.T) {
	const dnsHeaderOffset = 28
	for _, test := range []struct {
		name        string
		countOffset int
	}{
		{name: "authority", countOffset: dnsHeaderOffset + 8},
		{name: "additional", countOffset: dnsHeaderOffset + 10},
	} {
		t.Run(test.name, func(t *testing.T) {
			packet := buildDNSPacket()
			// The owner pointer is encoded correctly but targets byte 1 of the DNS
			// header, whose contents cannot form a complete DNS name. Merely skipping
			// the two-byte pointer would incorrectly accept this malformed record.
			packet = append(packet,
				0xc0, 0x01, // malformed compressed owner
				0x00, 0x0a, // TYPE NULL (ignored)
				0x00, 0x01, // CLASS IN
				0x00, 0x00, 0x00, 0x00, // TTL
				0x00, 0x00, // empty RDATA
			)
			binary.BigEndian.PutUint16(packet[test.countOffset:test.countOffset+2], 1)
			setDNSPacketLengths(packet)

			if got := parseDNSResponse(packet); len(got) != 0 {
				t.Fatalf("parseDNSResponse() = %#v, want malformed compressed %s owner rejected", got, test.name)
			}
		})
	}
}

func TestParseDNSResponseAcceptsValidCompressedIgnoredRecordOwner(t *testing.T) {
	packet := buildDNSPacket()
	packet = append(packet,
		0xc0, 0x0c, // owner points to the question name
		0x00, 0x0a, // TYPE NULL (ignored)
		0x00, 0x01, // CLASS IN
		0x00, 0x00, 0x00, 0x00, // TTL
		0x00, 0x00, // empty RDATA
	)
	binary.BigEndian.PutUint16(packet[38:40], 1) // ARCOUNT
	setDNSPacketLengths(packet)

	want := []dnsMapping{{IP: "1.2.3.4", Domain: "example.com", TTL: time.Minute}}
	if got := parseDNSResponse(packet); !reflect.DeepEqual(got, want) {
		t.Fatalf("parseDNSResponse() = %#v, want valid compressed additional owner accepted: %#v", got, want)
	}
}

func FuzzParseDNSResponse(f *testing.F) {
	f.Add(buildDNSPacket())
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, payload []byte) {
		for _, mapping := range parseDNSResponse(payload) {
			if mapping.IP == "" || mapping.Domain == "" || mapping.TTL < 0 {
				t.Fatalf("parser returned invalid mapping: %#v", mapping)
			}
		}
	})
}
