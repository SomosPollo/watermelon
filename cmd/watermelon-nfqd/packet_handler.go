//go:build linux

package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"net/netip"
	"sync"
	"time"

	nfqueue "github.com/florianl/go-nfqueue/v2"
	"github.com/saeta-eth/watermelon/internal/ask"
)

const (
	defaultVerdictCacheEntries    = 4096
	defaultRecentAllowOnceEntries = 4096
	defaultRecentAllowOnceTTL     = 2 * time.Minute
)

type verdictSetter interface {
	SetVerdict(id uint32, verdict int) error
}

type parsedTCPPacket struct {
	sourceIP        string
	sourcePort      int
	destinationPort int
	destinationIP   string
	sequence        uint32
}

type synFingerprint struct {
	sourceIP        string
	sourcePort      int
	destinationIP   string
	destinationPort int
	sequence        uint32
}

type recentAllowOnce struct {
	expiresAt time.Time
	sequence  uint64
}

func (p parsedTCPPacket) fingerprint() synFingerprint {
	return synFingerprint{
		sourceIP:        p.sourceIP,
		sourcePort:      p.sourcePort,
		destinationIP:   p.destinationIP,
		destinationPort: p.destinationPort,
		sequence:        p.sequence,
	}
}

// parseIPv4TCPPacket parses only the headers needed for a verdict request. It
// rejects malformed or incomplete packets rather than manufacturing port zero
// or reading a transport header at an invalid IPv4 IHL.
func parseIPv4TCPPacket(payload []byte) (parsedTCPPacket, bool) {
	if len(payload) < 20 || payload[0]>>4 != 4 {
		return parsedTCPPacket{}, false
	}
	ihl := int(payload[0]&0x0f) * 4
	if ihl < 20 || ihl > len(payload) || payload[9] != 6 { // TCP
		return parsedTCPPacket{}, false
	}
	if binary.BigEndian.Uint16(payload[6:8])&0x3fff != 0 { // fragmented
		return parsedTCPPacket{}, false
	}

	totalLength := int(binary.BigEndian.Uint16(payload[2:4]))
	// NFQUEUE may return only the configured snaplen. The declared packet may
	// therefore be longer than the capture, but every byte through the complete
	// TCP header must be both declared and present.
	if totalLength < ihl+20 || len(payload) < ihl+20 {
		return parsedTCPPacket{}, false
	}

	sourcePort := int(binary.BigEndian.Uint16(payload[ihl : ihl+2]))
	destinationPort := int(binary.BigEndian.Uint16(payload[ihl+2 : ihl+4]))
	if sourcePort == 0 || destinationPort == 0 {
		return parsedTCPPacket{}, false
	}
	tcpHeaderLength := int(payload[ihl+12]>>4) * 4
	if tcpHeaderLength < 20 || ihl+tcpHeaderLength > totalLength || ihl+tcpHeaderLength > len(payload) {
		return parsedTCPPacket{}, false
	}
	if payload[ihl+13]&0x02 == 0 { // SYN
		return parsedTCPPacket{}, false
	}

	source, sourceOK := netip.AddrFromSlice(payload[12:16])
	destination, destinationOK := netip.AddrFromSlice(payload[16:20])
	if !sourceOK || !source.Is4() || !destinationOK || !destination.Is4() {
		return parsedTCPPacket{}, false
	}
	return parsedTCPPacket{
		sourceIP:        source.String(),
		sourcePort:      sourcePort,
		destinationPort: destinationPort,
		destinationIP:   destination.String(),
		sequence:        binary.BigEndian.Uint32(payload[ihl+4 : ihl+8]),
	}, true
}

type tcpPacketHandler struct {
	queue          verdictSetter
	dns            *dnsAttributionCache
	requestVerdict func(ask.VerdictRequest) (string, bool)
	resolveProcess func(int) string
	logf           func(string, ...any)

	cacheMu sync.RWMutex
	cache   map[string]string
	// cacheOrder provides a bounded FIFO cache. Decisions are normally stable
	// for the daemon lifetime, so recency bookkeeping would add complexity
	// without materially improving the safety of eviction.
	cacheOrder      []string
	cacheNext       int
	maxCacheEntries int

	recentAllowOnce           map[synFingerprint]recentAllowOnce
	recentAllowOnceSequence   uint64
	maxRecentAllowOnceEntries int
	recentAllowOnceTTL        time.Duration
	now                       func() time.Time
}

func newTCPPacketHandler(
	queue verdictSetter,
	dns *dnsAttributionCache,
	requestVerdict func(ask.VerdictRequest) (string, bool),
	processResolver func(int) string,
) *tcpPacketHandler {
	return &tcpPacketHandler{
		queue:                     queue,
		dns:                       dns,
		requestVerdict:            requestVerdict,
		resolveProcess:            processResolver,
		logf:                      log.Printf,
		cache:                     make(map[string]string),
		maxCacheEntries:           defaultVerdictCacheEntries,
		recentAllowOnce:           make(map[synFingerprint]recentAllowOnce),
		maxRecentAllowOnceEntries: defaultRecentAllowOnceEntries,
		recentAllowOnceTTL:        defaultRecentAllowOnceTTL,
		now:                       time.Now,
	}
}

func (h *tcpPacketHandler) Handle(attribute nfqueue.Attribute) int {
	// Without an ID there is no packet on which a verdict can be set. Avoid
	// doing network or /proc work for a malformed netlink attribute.
	if attribute.PacketID == nil {
		return 0
	}
	packetID := *attribute.PacketID
	if attribute.Payload == nil {
		h.setVerdict(packetID, nfqueue.NfDrop)
		return 0
	}
	packet, ok := parseIPv4TCPPacket(*attribute.Payload)
	if !ok {
		h.setVerdict(packetID, nfqueue.NfDrop)
		return 0
	}

	// DNS attribution is display-only. A TCP SYN contains no hostname, so a
	// durable verdict must be scoped to the literal endpoint that the kernel can
	// actually enforce. This also prevents a later DNS observation from changing
	// the cache key for an already-decided endpoint.
	cacheKey := fmt.Sprintf("%s:%d", packet.destinationIP, packet.destinationPort)
	if verdict, ok := h.cachedVerdict(cacheKey); ok {
		h.applyVerdict(packetID, verdict)
		return 0
	}
	if h.isRecentAllowOnce(packet.fingerprint()) {
		h.applyVerdict(packetID, ask.VerdictAllowOnce)
		return 0
	}

	domain := packet.destinationIP
	if h.dns != nil {
		domain = h.dns.Destination(packet.destinationIP)
	}

	process := ""
	if h.resolveProcess != nil {
		process = h.resolveProcess(packet.sourcePort)
	}
	verdict := ask.VerdictBlock
	authenticated := false
	if h.requestVerdict != nil {
		verdict, authenticated = h.requestVerdict(ask.VerdictRequest{
			Domain:  domain,
			Port:    packet.destinationPort,
			Process: process,
			IP:      packet.destinationIP,
		})
	}
	if !authenticated {
		verdict = ask.VerdictBlock
	} else if verdict != ask.VerdictAllowOnce && verdict != ask.VerdictAlwaysAllow && verdict != ask.VerdictBlock {
		h.logf("verdict server returned unknown verdict %q (blocking)", verdict)
		verdict = ask.VerdictBlock
		authenticated = false
	}

	// Allow-once deliberately bypasses the endpoint cache. Retain only this exact
	// SYN fingerprint briefly so retransmissions of the already-approved attempt
	// do not reopen the dialog; a new source port or initial sequence prompts.
	// Fail-closed transport/authentication results are not cached: a temporary
	// absence of the host listener must not become a daemon-lifetime decision.
	if authenticated {
		if verdict == ask.VerdictAllowOnce {
			h.rememberAllowOnce(packet.fingerprint())
		} else {
			h.cacheVerdict(cacheKey, verdict)
		}
	}
	h.applyVerdict(packetID, verdict)
	return 0
}

func (h *tcpPacketHandler) cacheVerdict(key, verdict string) {
	h.cacheMu.Lock()
	defer h.cacheMu.Unlock()
	if h.maxCacheEntries <= 0 {
		return
	}
	if _, exists := h.cache[key]; !exists {
		if len(h.cacheOrder) >= h.maxCacheEntries {
			delete(h.cache, h.cacheOrder[h.cacheNext])
			h.cacheOrder[h.cacheNext] = key
			h.cacheNext = (h.cacheNext + 1) % h.maxCacheEntries
		} else {
			h.cacheOrder = append(h.cacheOrder, key)
		}
	}
	h.cache[key] = verdict
}

func (h *tcpPacketHandler) cachedVerdict(key string) (string, bool) {
	h.cacheMu.RLock()
	defer h.cacheMu.RUnlock()
	verdict, ok := h.cache[key]
	return verdict, ok
}

func (h *tcpPacketHandler) isRecentAllowOnce(fingerprint synFingerprint) bool {
	h.cacheMu.Lock()
	defer h.cacheMu.Unlock()
	now := h.currentTime()
	h.removeExpiredAllowOnceLocked(now)
	_, ok := h.recentAllowOnce[fingerprint]
	return ok
}

func (h *tcpPacketHandler) rememberAllowOnce(fingerprint synFingerprint) {
	h.cacheMu.Lock()
	defer h.cacheMu.Unlock()
	if h.maxRecentAllowOnceEntries <= 0 || h.recentAllowOnceTTL <= 0 {
		return
	}
	now := h.currentTime()
	h.removeExpiredAllowOnceLocked(now)
	if _, exists := h.recentAllowOnce[fingerprint]; !exists && len(h.recentAllowOnce) >= h.maxRecentAllowOnceEntries {
		var oldest synFingerprint
		var oldestSequence uint64
		found := false
		for candidate, entry := range h.recentAllowOnce {
			if !found || entry.sequence < oldestSequence {
				oldest = candidate
				oldestSequence = entry.sequence
				found = true
			}
		}
		if found {
			delete(h.recentAllowOnce, oldest)
		}
	}
	h.recentAllowOnceSequence++
	h.recentAllowOnce[fingerprint] = recentAllowOnce{
		expiresAt: now.Add(h.recentAllowOnceTTL),
		sequence:  h.recentAllowOnceSequence,
	}
}

func (h *tcpPacketHandler) removeExpiredAllowOnceLocked(now time.Time) {
	for fingerprint, entry := range h.recentAllowOnce {
		if !entry.expiresAt.After(now) {
			delete(h.recentAllowOnce, fingerprint)
		}
	}
}

func (h *tcpPacketHandler) currentTime() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}

func (h *tcpPacketHandler) applyVerdict(packetID uint32, verdict string) {
	if verdict == ask.VerdictBlock {
		h.setVerdict(packetID, nfqueue.NfDrop)
		return
	}
	h.setVerdict(packetID, nfqueue.NfAccept)
}

func (h *tcpPacketHandler) setVerdict(packetID uint32, verdict int) {
	if h.queue == nil {
		return
	}
	if err := h.queue.SetVerdict(packetID, verdict); err != nil {
		h.logf("set nfqueue verdict for packet %d: %v", packetID, err)
	}
}

func handleDNSPacket(queue verdictSetter, cache *dnsAttributionCache, attribute nfqueue.Attribute) int {
	if attribute.Payload != nil && cache != nil {
		cache.Observe(parseDNSResponse(*attribute.Payload))
	}
	if attribute.PacketID != nil && queue != nil {
		if err := queue.SetVerdict(*attribute.PacketID, nfqueue.NfAccept); err != nil {
			log.Printf("set DNS nfqueue verdict for packet %d: %v", *attribute.PacketID, err)
		}
	}
	return 0
}
