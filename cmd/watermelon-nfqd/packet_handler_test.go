//go:build linux

package main

import (
	"encoding/binary"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	nfqueue "github.com/florianl/go-nfqueue/v2"
	"github.com/saeta-eth/watermelon/internal/ask"
)

type verdictCall struct {
	packetID uint32
	verdict  int
}

type recordingVerdictSetter struct {
	mu    sync.Mutex
	calls []verdictCall
	err   error
}

func (s *recordingVerdictSetter) SetVerdict(packetID uint32, verdict int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, verdictCall{packetID: packetID, verdict: verdict})
	return s.err
}

func (s *recordingVerdictSetter) Calls() []verdictCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]verdictCall(nil), s.calls...)
}

func buildTCPPacket(sourcePort, destinationPort uint16, destination [4]byte) []byte {
	packet := make([]byte, 40)
	packet[0] = 0x45
	binary.BigEndian.PutUint16(packet[2:4], uint16(len(packet)))
	packet[8] = 64
	packet[9] = 6
	copy(packet[12:16], []byte{10, 0, 0, 2})
	copy(packet[16:20], destination[:])
	binary.BigEndian.PutUint16(packet[20:22], sourcePort)
	binary.BigEndian.PutUint16(packet[22:24], destinationPort)
	packet[32] = 0x50 // TCP data offset: 20 bytes
	packet[33] = 0x02 // SYN
	return packet
}

func nfqueueAttribute(packetID uint32, payload []byte) nfqueue.Attribute {
	return nfqueue.Attribute{PacketID: &packetID, Payload: &payload}
}

func TestParseIPv4TCPPacket(t *testing.T) {
	packet := buildTCPPacket(49152, 443, [4]byte{203, 0, 113, 10})
	got, ok := parseIPv4TCPPacket(packet)
	if !ok {
		t.Fatal("parseIPv4TCPPacket() rejected a valid SYN")
	}
	want := parsedTCPPacket{
		sourceIP:        "10.0.0.2",
		sourcePort:      49152,
		destinationPort: 443,
		destinationIP:   "203.0.113.10",
	}
	if got != want {
		t.Fatalf("packet = %#v, want %#v", got, want)
	}
}

func TestParseIPv4TCPPacketRejectsMalformedInput(t *testing.T) {
	tests := []struct {
		name   string
		packet func() []byte
	}{
		{"nil", func() []byte { return nil }},
		{"truncated IPv4", func() []byte { return make([]byte, 19) }},
		{"not IPv4", func() []byte { packet := buildTCPPacket(1, 1, [4]byte{}); packet[0] = 0x65; return packet }},
		{"IHL below minimum", func() []byte { packet := buildTCPPacket(1, 1, [4]byte{}); packet[0] = 0x44; return packet }},
		{"IHL beyond packet", func() []byte { packet := buildTCPPacket(1, 1, [4]byte{}); packet[0] = 0x4f; return packet }},
		{"not TCP", func() []byte { packet := buildTCPPacket(1, 1, [4]byte{}); packet[9] = 17; return packet }},
		{"fragmented", func() []byte { packet := buildTCPPacket(1, 1, [4]byte{}); packet[6] = 0x20; return packet }},
		{"zero total length", func() []byte {
			packet := buildTCPPacket(1, 1, [4]byte{})
			binary.BigEndian.PutUint16(packet[2:4], 0)
			return packet
		}},
		{"total length below headers", func() []byte {
			packet := buildTCPPacket(1, 1, [4]byte{})
			binary.BigEndian.PutUint16(packet[2:4], 39)
			return packet
		}},
		{"zero source port", func() []byte { return buildTCPPacket(0, 443, [4]byte{}) }},
		{"zero destination port", func() []byte { return buildTCPPacket(49152, 0, [4]byte{}) }},
		{"TCP data offset below minimum", func() []byte { packet := buildTCPPacket(1, 1, [4]byte{}); packet[32] = 0x40; return packet }},
		{"TCP data offset beyond packet", func() []byte { packet := buildTCPPacket(1, 1, [4]byte{}); packet[32] = 0xf0; return packet }},
		{"not SYN", func() []byte { packet := buildTCPPacket(1, 1, [4]byte{}); packet[33] = 0x10; return packet }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got, ok := parseIPv4TCPPacket(test.packet()); ok {
				t.Fatalf("parseIPv4TCPPacket() = %#v, true; want rejection", got)
			}
		})
	}
}

func TestTCPPacketHandlerDropsMalformedAttributesWithoutPrompting(t *testing.T) {
	queue := &recordingVerdictSetter{}
	requests := 0
	resolutions := 0
	handler := newTCPPacketHandler(queue, nil, func(ask.VerdictRequest) (string, bool) {
		requests++
		return ask.VerdictAlwaysAllow, true
	}, func(int) string {
		resolutions++
		return "should-not-run"
	})
	handler.logf = func(string, ...any) {}

	packetID := uint32(7)
	handler.Handle(nfqueue.Attribute{PacketID: &packetID})
	short := []byte{0x45}
	handler.Handle(nfqueue.Attribute{PacketID: &packetID, Payload: &short})
	handler.Handle(nfqueue.Attribute{Payload: &short}) // no ID: cannot set a verdict

	wantCalls := []verdictCall{
		{packetID: 7, verdict: nfqueue.NfDrop},
		{packetID: 7, verdict: nfqueue.NfDrop},
	}
	if got := queue.Calls(); !reflect.DeepEqual(got, wantCalls) {
		t.Fatalf("verdict calls = %#v, want %#v", got, wantCalls)
	}
	if requests != 0 || resolutions != 0 {
		t.Fatalf("malformed attributes made %d requests and %d process resolutions", requests, resolutions)
	}
}

func TestTCPPacketHandlerBuildsAttributedRequest(t *testing.T) {
	queue := &recordingVerdictSetter{}
	dns := newDNSAttributionCache(10, time.Hour)
	dns.Observe([]dnsMapping{{
		IP: "203.0.113.10", Domain: "registry.example", TTL: time.Minute,
	}})
	var gotRequest ask.VerdictRequest
	handler := newTCPPacketHandler(queue, dns, func(request ask.VerdictRequest) (string, bool) {
		gotRequest = request
		return ask.VerdictAllowOnce, true
	}, func(port int) string {
		if port != 49152 {
			t.Fatalf("process source port = %d, want 49152", port)
		}
		return "npm"
	})

	handler.Handle(nfqueueAttribute(42, buildTCPPacket(49152, 443, [4]byte{203, 0, 113, 10})))
	wantRequest := ask.VerdictRequest{
		Domain: "registry.example", Port: 443, Process: "npm", IP: "203.0.113.10",
	}
	if gotRequest.Domain != wantRequest.Domain || gotRequest.Port != wantRequest.Port ||
		gotRequest.Process != wantRequest.Process || gotRequest.IP != wantRequest.IP {
		t.Fatalf("request = %#v, want core fields %#v", gotRequest, wantRequest)
	}
	if got := queue.Calls(); !reflect.DeepEqual(got, []verdictCall{{packetID: 42, verdict: nfqueue.NfAccept}}) {
		t.Fatalf("verdict calls = %#v, want accept", got)
	}
}

func TestTCPPacketHandlerVerdictCacheSemantics(t *testing.T) {
	tests := []struct {
		name         string
		verdict      string
		wantRequests int
		wantKernel   int
	}{
		{"always allow is cached", ask.VerdictAlwaysAllow, 1, nfqueue.NfAccept},
		{"block is cached", ask.VerdictBlock, 1, nfqueue.NfDrop},
		{"allow once suppresses an identical retransmission", ask.VerdictAllowOnce, 1, nfqueue.NfAccept},
		{"unknown verdict fails closed without caching", "surprise", 2, nfqueue.NfDrop},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			queue := &recordingVerdictSetter{}
			requests := 0
			resolutions := 0
			handler := newTCPPacketHandler(queue, nil, func(ask.VerdictRequest) (string, bool) {
				requests++
				return test.verdict, true
			}, func(int) string {
				resolutions++
				return "curl"
			})
			handler.logf = func(string, ...any) {}
			packet := buildTCPPacket(49152, 443, [4]byte{203, 0, 113, 10})
			handler.Handle(nfqueueAttribute(1, packet))
			handler.Handle(nfqueueAttribute(2, packet))

			if requests != test.wantRequests || resolutions != test.wantRequests {
				t.Fatalf("requests/resolutions = %d/%d, want %d/%d", requests, resolutions, test.wantRequests, test.wantRequests)
			}
			wantCalls := []verdictCall{{packetID: 1, verdict: test.wantKernel}, {packetID: 2, verdict: test.wantKernel}}
			if got := queue.Calls(); !reflect.DeepEqual(got, wantCalls) {
				t.Fatalf("verdict calls = %#v, want %#v", got, wantCalls)
			}
		})
	}
}

func TestTCPPacketHandlerAllowOnceSeparatesNewConnections(t *testing.T) {
	queue := &recordingVerdictSetter{}
	requests := 0
	resolutions := 0
	handler := newTCPPacketHandler(queue, nil, func(ask.VerdictRequest) (string, bool) {
		requests++
		return ask.VerdictAllowOnce, true
	}, func(int) string {
		resolutions++
		return "curl"
	})

	first := buildTCPPacket(49152, 443, [4]byte{203, 0, 113, 10})
	binary.BigEndian.PutUint32(first[24:28], 100)
	retransmission := append([]byte(nil), first...)
	newSequence := append([]byte(nil), first...)
	binary.BigEndian.PutUint32(newSequence[24:28], 101)
	newSourcePort := append([]byte(nil), first...)
	binary.BigEndian.PutUint16(newSourcePort[20:22], 49153)

	for packetID, packet := range [][]byte{first, retransmission, newSequence, newSourcePort} {
		handler.Handle(nfqueueAttribute(uint32(packetID+1), packet))
	}
	if requests != 3 || resolutions != 3 {
		t.Fatalf("requests/resolutions = %d/%d, want one prompt suppressed only for the exact retransmission", requests, resolutions)
	}
	if got := queue.Calls(); len(got) != 4 {
		t.Fatalf("verdict calls = %#v, want four accepted SYN packets", got)
	} else {
		for _, call := range got {
			if call.verdict != nfqueue.NfAccept {
				t.Fatalf("verdict calls = %#v, want every allow-once SYN accepted", got)
			}
		}
	}
}

func TestTCPPacketHandlerAllowOnceFingerprintExpires(t *testing.T) {
	queue := &recordingVerdictSetter{}
	requests := 0
	now := time.Unix(1_700_000_000, 0)
	handler := newTCPPacketHandler(queue, nil, func(ask.VerdictRequest) (string, bool) {
		requests++
		return ask.VerdictAllowOnce, true
	}, nil)
	handler.now = func() time.Time { return now }
	handler.recentAllowOnceTTL = time.Minute
	packet := buildTCPPacket(49152, 443, [4]byte{203, 0, 113, 10})
	binary.BigEndian.PutUint32(packet[24:28], 100)

	handler.Handle(nfqueueAttribute(1, packet))
	handler.Handle(nfqueueAttribute(2, packet))
	if requests != 1 {
		t.Fatalf("requests before expiry = %d, want one", requests)
	}
	now = now.Add(time.Minute)
	handler.Handle(nfqueueAttribute(3, packet))
	if requests != 2 {
		t.Fatalf("requests after expiry = %d, want a new prompt", requests)
	}
}

func TestTCPPacketHandlerBoundsAllowOnceFingerprints(t *testing.T) {
	queue := &recordingVerdictSetter{}
	requests := 0
	handler := newTCPPacketHandler(queue, nil, func(ask.VerdictRequest) (string, bool) {
		requests++
		return ask.VerdictAllowOnce, true
	}, nil)
	handler.maxRecentAllowOnceEntries = 2

	packets := make([][]byte, 3)
	for index := range packets {
		packets[index] = buildTCPPacket(49152, 443, [4]byte{203, 0, 113, 10})
		binary.BigEndian.PutUint32(packets[index][24:28], uint32(index+1))
		handler.Handle(nfqueueAttribute(uint32(index+1), packets[index]))
	}
	handler.Handle(nfqueueAttribute(4, packets[0])) // oldest fingerprint was evicted

	if requests != 4 {
		t.Fatalf("requests = %d, want oldest bounded fingerprint to prompt again", requests)
	}
	handler.cacheMu.RLock()
	defer handler.cacheMu.RUnlock()
	if len(handler.recentAllowOnce) != 2 {
		t.Fatalf("allow-once fingerprint count = %d, want hard cap of 2", len(handler.recentAllowOnce))
	}
}

func TestTCPPacketHandlerAcceptsSnaplenTruncatedSYNPayload(t *testing.T) {
	queue := &recordingVerdictSetter{}
	requests := 0
	handler := newTCPPacketHandler(queue, nil, func(ask.VerdictRequest) (string, bool) {
		requests++
		return ask.VerdictAllowOnce, true
	}, nil)
	packet := buildTCPPacket(49152, 443, [4]byte{203, 0, 113, 10})
	binary.BigEndian.PutUint16(packet[2:4], 512) // payload extends beyond NFQUEUE's captured snaplen

	handler.Handle(nfqueueAttribute(1, packet))
	if requests != 1 {
		t.Fatalf("requests = %d, want captured complete headers to reach verdict server", requests)
	}
	if got := queue.Calls(); !reflect.DeepEqual(got, []verdictCall{{packetID: 1, verdict: nfqueue.NfAccept}}) {
		t.Fatalf("verdict calls = %#v, want accept", got)
	}
}

func TestTCPPacketHandlerCacheSeparatesDestinations(t *testing.T) {
	queue := &recordingVerdictSetter{err: errors.New("test netlink failure")}
	requests := 0
	handler := newTCPPacketHandler(queue, nil, func(ask.VerdictRequest) (string, bool) {
		requests++
		return ask.VerdictAlwaysAllow, true
	}, nil)
	handler.logf = func(string, ...any) {}
	handler.Handle(nfqueueAttribute(1, buildTCPPacket(49152, 443, [4]byte{203, 0, 113, 10})))
	handler.Handle(nfqueueAttribute(2, buildTCPPacket(49152, 8443, [4]byte{203, 0, 113, 10})))
	handler.Handle(nfqueueAttribute(3, buildTCPPacket(49152, 443, [4]byte{203, 0, 113, 11})))
	if requests != 3 {
		t.Fatalf("requests = %d, want one per IP/port destination", requests)
	}
}

func TestTCPPacketHandlerReusesEndpointVerdictAfterHostnameChanges(t *testing.T) {
	queue := &recordingVerdictSetter{}
	dns := newDNSAttributionCache(10, time.Hour)
	ip := "203.0.113.10"
	dns.Observe([]dnsMapping{{IP: ip, Domain: "first.example", TTL: time.Minute}})

	requests := 0
	var promptedDomain string
	handler := newTCPPacketHandler(queue, dns, func(request ask.VerdictRequest) (string, bool) {
		requests++
		promptedDomain = request.Domain
		return ask.VerdictAlwaysAllow, true
	}, nil)
	packet := buildTCPPacket(49152, 443, [4]byte{203, 0, 113, 10})
	handler.Handle(nfqueueAttribute(1, packet))

	// Change the informational DNS attribution before the next SYN. The kernel
	// can enforce only the literal IP and port, so this endpoint must retain its
	// prior verdict rather than become a second hostname-scoped decision.
	dns.Observe([]dnsMapping{
		{IP: ip, Domain: "first.example", TTL: 0},
		{IP: ip, Domain: "second.example", TTL: time.Minute},
	})
	if got := dns.Destination(ip); got != "second.example" {
		t.Fatalf("updated DNS attribution = %q, want second.example", got)
	}
	handler.Handle(nfqueueAttribute(2, packet))

	if requests != 1 || promptedDomain != "first.example" {
		t.Fatalf("requests/domain = %d/%q, want one initial first.example prompt", requests, promptedDomain)
	}
	wantCalls := []verdictCall{
		{packetID: 1, verdict: nfqueue.NfAccept},
		{packetID: 2, verdict: nfqueue.NfAccept},
	}
	if got := queue.Calls(); !reflect.DeepEqual(got, wantCalls) {
		t.Fatalf("verdict calls = %#v, want endpoint verdict reused: %#v", got, wantCalls)
	}
}

func TestTCPPacketHandlerBoundsVerdictCache(t *testing.T) {
	queue := &recordingVerdictSetter{}
	requests := 0
	handler := newTCPPacketHandler(queue, nil, func(ask.VerdictRequest) (string, bool) {
		requests++
		return ask.VerdictAlwaysAllow, true
	}, nil)
	handler.maxCacheEntries = 2

	first := buildTCPPacket(49152, 443, [4]byte{203, 0, 113, 1})
	second := buildTCPPacket(49152, 443, [4]byte{203, 0, 113, 2})
	third := buildTCPPacket(49152, 443, [4]byte{203, 0, 113, 3})
	handler.Handle(nfqueueAttribute(1, first))
	handler.Handle(nfqueueAttribute(2, second))
	handler.Handle(nfqueueAttribute(3, third))
	handler.Handle(nfqueueAttribute(4, second)) // retained
	handler.Handle(nfqueueAttribute(5, first))  // oldest was evicted

	if requests != 4 {
		t.Fatalf("requests = %d, want 4 after bounded-cache eviction", requests)
	}
	handler.cacheMu.RLock()
	defer handler.cacheMu.RUnlock()
	if len(handler.cache) != 2 || len(handler.cacheOrder) != 2 {
		t.Fatalf("cache/map lengths = %d/%d, want hard cap of 2", len(handler.cache), len(handler.cacheOrder))
	}
}

func TestTCPPacketHandlerDoesNotCacheTransientFailClosedResult(t *testing.T) {
	queue := &recordingVerdictSetter{}
	requests := 0
	handler := newTCPPacketHandler(queue, nil, func(ask.VerdictRequest) (string, bool) {
		requests++
		if requests == 1 {
			return ask.VerdictBlock, false // listener unavailable/authentication failure
		}
		return ask.VerdictAlwaysAllow, true
	}, nil)
	packet := buildTCPPacket(49152, 443, [4]byte{203, 0, 113, 10})

	handler.Handle(nfqueueAttribute(1, packet))
	handler.Handle(nfqueueAttribute(2, packet))
	handler.Handle(nfqueueAttribute(3, packet))

	if requests != 2 {
		t.Fatalf("requests = %d, want retry after transient failure then cached explicit verdict", requests)
	}
	wantCalls := []verdictCall{
		{packetID: 1, verdict: nfqueue.NfDrop},
		{packetID: 2, verdict: nfqueue.NfAccept},
		{packetID: 3, verdict: nfqueue.NfAccept},
	}
	if got := queue.Calls(); !reflect.DeepEqual(got, wantCalls) {
		t.Fatalf("verdict calls = %#v, want %#v", got, wantCalls)
	}
}

func TestTCPPacketHandlerRejectsUnauthenticatedAllow(t *testing.T) {
	queue := &recordingVerdictSetter{}
	handler := newTCPPacketHandler(queue, nil, func(ask.VerdictRequest) (string, bool) {
		return ask.VerdictAlwaysAllow, false
	}, nil)
	packet := buildTCPPacket(49152, 443, [4]byte{203, 0, 113, 10})
	handler.Handle(nfqueueAttribute(1, packet))

	if got := queue.Calls(); !reflect.DeepEqual(got, []verdictCall{{packetID: 1, verdict: nfqueue.NfDrop}}) {
		t.Fatalf("verdict calls = %#v, want fail-closed drop", got)
	}
	if _, ok := handler.cachedVerdict("203.0.113.10:443"); ok {
		t.Fatal("unauthenticated result was cached")
	}
}

func TestDNSPacketHandlerAlwaysAccepts(t *testing.T) {
	queue := &recordingVerdictSetter{}
	cache := newDNSAttributionCache(10, time.Hour)
	packet := buildDNSPacket()
	handleDNSPacket(queue, cache, nfqueueAttribute(9, packet))
	if got := queue.Calls(); !reflect.DeepEqual(got, []verdictCall{{packetID: 9, verdict: nfqueue.NfAccept}}) {
		t.Fatalf("DNS verdict calls = %#v, want accept", got)
	}
	if got := cache.Destination("1.2.3.4"); got != "example.com" {
		t.Fatalf("DNS attribution = %q, want example.com", got)
	}
}

func FuzzParseIPv4TCPPacket(f *testing.F) {
	f.Add(buildTCPPacket(49152, 443, [4]byte{203, 0, 113, 10}))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, payload []byte) {
		packet, ok := parseIPv4TCPPacket(payload)
		if ok && (packet.sourcePort < 1 || packet.destinationPort < 1 || packet.destinationIP == "") {
			t.Fatalf("successful parse returned invalid fields: %#v", packet)
		}
	})
}
