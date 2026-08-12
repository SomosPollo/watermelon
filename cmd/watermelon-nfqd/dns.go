package main

import (
	"encoding/binary"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	defaultDNSCacheEntries = 4096
	defaultDNSCacheMaxTTL  = time.Hour
	// IPv4's 16-bit total-length field includes its header. DNS responses can
	// legitimately exceed the old 512-byte NFQUEUE copy limit, so copy the full
	// IPv4 packet and let the bounded parser validate its UDP/DNS lengths.
	dnsQueueMaxPacketLen uint32 = 1<<16 - 1
)

// dnsMapping is an attribution learned from an A record. The TTL comes from
// the DNS response so an old answer cannot label an unrelated connection
// forever.
type dnsMapping struct {
	IP     string
	Domain string
	TTL    time.Duration
}

type dnsAttribution struct {
	expiresAt time.Time
	sequence  uint64
}

// dnsAmbiguity is a collapsed IP bucket. When more aliases for one address
// are live than the bounded cache can retain individually, keeping any one of
// them would create false certainty. One counted sentinel instead preserves
// the safe direct-IP fallback without allowing side-state to grow unbounded.
type dnsAmbiguity struct {
	expiresAt time.Time
	sequence  uint64
}

// dnsAttributionCache keeps all live hostnames observed for an IP. A lookup is
// deliberately considered ambiguous when an IP has more than one live name;
// callers then fall back to displaying the IP instead of guessing a hostname.
//
// The entry cap protects the long-running root daemon from unbounded growth.
type dnsAttributionCache struct {
	mu         sync.Mutex
	entries    map[string]map[string]dnsAttribution
	ambiguous  map[string]dnsAmbiguity
	entryCount int
	maxEntries int
	maxTTL     time.Duration
	sequence   uint64
	now        func() time.Time
}

func newDNSAttributionCache(maxEntries int, maxTTL time.Duration) *dnsAttributionCache {
	return &dnsAttributionCache{
		entries:    make(map[string]map[string]dnsAttribution),
		ambiguous:  make(map[string]dnsAmbiguity),
		maxEntries: maxEntries,
		maxTTL:     maxTTL,
		now:        time.Now,
	}
}

func (c *dnsAttributionCache) Observe(mappings []dnsMapping) {
	if c == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	c.removeExpiredLocked(now)
	for _, mapping := range mappings {
		ip := strings.TrimSpace(mapping.IP)
		domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(mapping.Domain)), ".")
		if ip == "" || domain == "" {
			continue
		}

		if mapping.TTL <= 0 || c.maxEntries <= 0 || c.maxTTL <= 0 {
			c.removeLocked(ip, domain)
			continue
		}
		ttl := min(mapping.TTL, c.maxTTL)
		expiresAt := now.Add(ttl)
		if ambiguity, exists := c.ambiguous[ip]; exists {
			if expiresAt.After(ambiguity.expiresAt) {
				ambiguity.expiresAt = expiresAt
			}
			c.sequence++
			ambiguity.sequence = c.sequence
			c.ambiguous[ip] = ambiguity
			continue
		}

		domains := c.entries[ip]
		if _, exists := domains[domain]; !exists {
			for c.entryCount >= c.maxEntries {
				// Evict an entire IP bucket, never one alias. Removing only one
				// name from a shared address could make the remaining name look
				// uniquely attributable. Keep the bucket being extended so its
				// ambiguity is not lost under capacity pressure.
				if !c.evictOldestIPLocked(ip) {
					break
				}
			}
			if c.entryCount >= c.maxEntries {
				// This IP itself exhausts the mapping budget. Collapse all of its
				// aliases to one counted ambiguity sentinel rather than discarding
				// just one name and making another look uniquely attributable.
				c.collapseIPLocked(ip, expiresAt)
				continue
			}
			// Whole-bucket eviction may have removed unrelated entries.
			domains = c.entries[ip]
			if domains == nil {
				domains = make(map[string]dnsAttribution)
				c.entries[ip] = domains
			}
			c.entryCount++
		}
		c.sequence++
		domains[domain] = dnsAttribution{
			expiresAt: expiresAt,
			sequence:  c.sequence,
		}
	}
}

// Destination returns the sole live hostname attributed to ip. Expired,
// absent, and shared-IP entries return ip. A TCP packet cannot reveal whether
// the workload used that observed hostname or the literal address, so callers
// use this only as prompt metadata and scope decisions to the IP endpoint.
func (c *dnsAttributionCache) Destination(ip string) string {
	if c == nil {
		return ip
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	c.removeExpiredLocked(now)
	if _, ambiguous := c.ambiguous[ip]; ambiguous {
		return ip
	}
	domains := c.entries[ip]
	if len(domains) != 1 {
		return ip
	}
	for domain := range domains {
		return domain
	}
	return ip
}

func (c *dnsAttributionCache) removeExpiredLocked(now time.Time) {
	for ip, domains := range c.entries {
		for domain, attribution := range domains {
			if !attribution.expiresAt.After(now) {
				c.removeLocked(ip, domain)
			}
		}
	}
	for ip, ambiguity := range c.ambiguous {
		if !ambiguity.expiresAt.After(now) {
			delete(c.ambiguous, ip)
			c.entryCount--
		}
	}
}

func (c *dnsAttributionCache) removeLocked(ip, domain string) {
	domains := c.entries[ip]
	if domains == nil {
		return
	}
	if _, exists := domains[domain]; !exists {
		return
	}
	delete(domains, domain)
	c.entryCount--
	if len(domains) == 0 {
		delete(c.entries, ip)
	}
}

func (c *dnsAttributionCache) collapseIPLocked(ip string, additionalExpiry time.Time) {
	expiresAt := additionalExpiry
	if domains := c.entries[ip]; domains != nil {
		for _, attribution := range domains {
			if attribution.expiresAt.After(expiresAt) {
				expiresAt = attribution.expiresAt
			}
		}
		c.entryCount -= len(domains)
		delete(c.entries, ip)
	}
	if existing, exists := c.ambiguous[ip]; exists {
		if existing.expiresAt.After(expiresAt) {
			expiresAt = existing.expiresAt
		}
	} else {
		c.entryCount++
	}
	c.sequence++
	c.ambiguous[ip] = dnsAmbiguity{expiresAt: expiresAt, sequence: c.sequence}
}

func (c *dnsAttributionCache) evictOldestIPLocked(excludeIP string) bool {
	var oldestIP string
	var oldestSequence uint64
	found := false
	for ip, domains := range c.entries {
		if ip == excludeIP {
			continue
		}
		for _, attribution := range domains {
			if !found || attribution.sequence < oldestSequence {
				oldestIP = ip
				oldestSequence = attribution.sequence
				found = true
			}
		}
	}
	for ip, ambiguity := range c.ambiguous {
		if ip == excludeIP {
			continue
		}
		if !found || ambiguity.sequence < oldestSequence {
			oldestIP = ip
			oldestSequence = ambiguity.sequence
			found = true
		}
	}
	if found {
		c.entryCount -= len(c.entries[oldestIP])
		delete(c.entries, oldestIP)
		if _, exists := c.ambiguous[oldestIP]; exists {
			delete(c.ambiguous, oldestIP)
			c.entryCount--
		}
	}
	return found
}

const (
	dnsTypeA     = 1
	dnsTypeCNAME = 5
	dnsClassIN   = 1

	dnsFlagResponse  = 0x8000
	dnsOpcodeMask    = 0x7800
	dnsFlagTruncated = 0x0200
	dnsReservedZ     = 0x0040
	dnsRCodeMask     = 0x000f
)

type dnsCNAMERecord struct {
	target string
	ttl    uint32
}

type dnsAddressRecord struct {
	ip  string
	ttl uint32
}

// parseDNSResponse extracts endpoint attribution only for the response's one
// A/IN question. It follows that question's CNAME chain and ignores unrelated
// answer records, preventing an answer-section injection from attributing an
// arbitrary hostname to an IP. Malformed, fragmented, truncated, and failed
// responses yield no mappings.
func parseDNSResponse(payload []byte) []dnsMapping {
	if len(payload) < 20 || payload[0]>>4 != 4 {
		return nil
	}

	ihl := int(payload[0]&0x0f) * 4
	if ihl < 20 || ihl > len(payload) || payload[9] != 17 { // UDP
		return nil
	}
	if binary.BigEndian.Uint16(payload[6:8])&0x3fff != 0 { // fragmented
		return nil
	}

	totalLength := int(binary.BigEndian.Uint16(payload[2:4]))
	if totalLength < ihl+8 || totalLength > len(payload) {
		return nil
	}

	if binary.BigEndian.Uint16(payload[ihl:ihl+2]) != 53 {
		return nil
	}
	udpLength := int(binary.BigEndian.Uint16(payload[ihl+4 : ihl+6]))
	if udpLength < 8+12 || ihl+udpLength > totalLength {
		return nil
	}
	dns := payload[ihl+8 : ihl+udpLength]
	flags := binary.BigEndian.Uint16(dns[2:4])
	if flags&dnsFlagResponse == 0 ||
		flags&dnsOpcodeMask != 0 ||
		flags&dnsFlagTruncated != 0 ||
		flags&dnsReservedZ != 0 ||
		flags&dnsRCodeMask != 0 {
		return nil
	}

	qdCount := int(binary.BigEndian.Uint16(dns[4:6]))
	anCount := int(binary.BigEndian.Uint16(dns[6:8]))
	nsCount := int(binary.BigEndian.Uint16(dns[8:10]))
	arCount := int(binary.BigEndian.Uint16(dns[10:12]))
	if qdCount != 1 || anCount == 0 {
		return nil
	}

	offset := 12
	questionName, valid := canonicalDNSHostname(readDNSName(dns, offset))
	offset = skipDNSName(dns, offset)
	if !valid || offset < 0 || offset+4 > len(dns) {
		return nil
	}
	questionType := binary.BigEndian.Uint16(dns[offset : offset+2])
	questionClass := binary.BigEndian.Uint16(dns[offset+2 : offset+4])
	if questionType != dnsTypeA || questionClass != dnsClassIN {
		return nil
	}
	offset += 4

	cnames := make(map[string]dnsCNAMERecord)
	conflictingCNAME := make(map[string]bool)
	addresses := make(map[string][]dnsAddressRecord)
	for i := 0; i < anCount; i++ {
		name, nameValid := canonicalDNSHostname(readDNSName(dns, offset))
		offset = skipDNSName(dns, offset)
		if !nameValid || offset < 0 || offset+10 > len(dns) {
			return nil
		}
		qtype := binary.BigEndian.Uint16(dns[offset : offset+2])
		qclass := binary.BigEndian.Uint16(dns[offset+2 : offset+4])
		ttl := binary.BigEndian.Uint32(dns[offset+4 : offset+8])
		rdLength := int(binary.BigEndian.Uint16(dns[offset+8 : offset+10]))
		offset += 10
		if rdLength > len(dns)-offset {
			return nil
		}
		rdataEnd := offset + rdLength

		switch {
		case qclass == dnsClassIN && qtype == dnsTypeA:
			if rdLength != 4 {
				return nil
			}
			addresses[name] = append(addresses[name], dnsAddressRecord{
				ip:  fmt.Sprintf("%d.%d.%d.%d", dns[offset], dns[offset+1], dns[offset+2], dns[offset+3]),
				ttl: ttl,
			})
		case qclass == dnsClassIN && qtype == dnsTypeCNAME:
			target, targetValid := canonicalDNSHostname(readDNSName(dns, offset))
			encodedEnd := skipDNSName(dns, offset)
			if !targetValid || encodedEnd != rdataEnd {
				return nil
			}
			if existing, ok := cnames[name]; ok {
				if existing.target != target {
					conflictingCNAME[name] = true
				} else if ttl < existing.ttl {
					existing.ttl = ttl
					cnames[name] = existing
				}
			} else {
				cnames[name] = dnsCNAMERecord{target: target, ttl: ttl}
			}
		}
		offset = rdataEnd
	}
	// Authority and additional records are not used for attribution, but they
	// are still part of the declared DNS message and must be structurally valid.
	for i := 0; i < nsCount+arCount; i++ {
		_, ownerValid := readDNSNameChecked(dns, offset)
		offset = skipDNSName(dns, offset)
		if !ownerValid || offset < 0 || offset+10 > len(dns) {
			return nil
		}
		rdLength := int(binary.BigEndian.Uint16(dns[offset+8 : offset+10]))
		offset += 10
		if rdLength > len(dns)-offset {
			return nil
		}
		offset += rdLength
	}
	if offset != len(dns) {
		return nil
	}

	terminal := questionName
	chainTTL := ^uint32(0)
	hasCNAME := false
	visited := make(map[string]bool)
	for {
		if visited[terminal] || conflictingCNAME[terminal] {
			return nil
		}
		visited[terminal] = true
		cname, ok := cnames[terminal]
		if !ok {
			break
		}
		// A CNAME owner cannot also be the terminal A owner. Treat such a
		// contradictory answer as ambiguous rather than selecting one branch.
		if len(addresses[terminal]) != 0 {
			return nil
		}
		hasCNAME = true
		chainTTL = min(chainTTL, cname.ttl)
		terminal = cname.target
	}

	terminalAddresses := addresses[terminal]
	if len(terminalAddresses) == 0 {
		return nil
	}
	result := make([]dnsMapping, 0, len(terminalAddresses))
	resultIndex := make(map[string]int, len(terminalAddresses))
	for _, address := range terminalAddresses {
		ttl := address.ttl
		if hasCNAME {
			ttl = min(ttl, chainTTL)
		}
		mapping := dnsMapping{
			IP:     address.ip,
			Domain: questionName,
			TTL:    time.Duration(ttl) * time.Second,
		}
		if index, duplicate := resultIndex[address.ip]; duplicate {
			if mapping.TTL < result[index].TTL {
				result[index].TTL = mapping.TTL
			}
			continue
		}
		resultIndex[address.ip] = len(result)
		result = append(result, mapping)
	}
	return result
}

func canonicalDNSHostname(name string) (string, bool) {
	name = strings.ToLower(name)
	if name == "" || len(name) > 253 {
		return "", false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}
		for _, char := range []byte(label) {
			if (char < 'a' || char > 'z') &&
				(char < '0' || char > '9') && char != '-' {
				return "", false
			}
		}
	}
	return name, true
}

func skipDNSName(dns []byte, offset int) int {
	if offset < 0 {
		return -1
	}
	for offset < len(dns) {
		length := int(dns[offset])
		switch {
		case length == 0:
			return offset + 1
		case length&0xc0 == 0xc0:
			if offset+1 >= len(dns) {
				return -1
			}
			ptr := int(binary.BigEndian.Uint16(dns[offset:offset+2])) & 0x3fff
			if ptr >= offset || ptr >= len(dns) {
				return -1
			}
			return offset + 2
		case length&0xc0 != 0 || length > 63 || offset+1+length > len(dns):
			return -1
		default:
			offset += 1 + length
		}
	}
	return -1
}

const maxDNSCompressionPointers = 128

func readDNSName(dns []byte, offset int) string {
	name, _ := readDNSNameChecked(dns, offset)
	return name
}

// readDNSNameChecked distinguishes a valid root name from malformed input.
// readDNSName retains its historical string-only API for callers and tests,
// while packet parsing uses this form whenever an empty root name is legal.
func readDNSNameChecked(dns []byte, offset int) (string, bool) {
	if offset < 0 {
		return "", false
	}
	var parts []string
	seen := make(map[int]bool)
	expandedLength := 0
	pointerHops := 0
	for offset < len(dns) {
		if seen[offset] {
			return "", false
		}
		seen[offset] = true
		length := int(dns[offset])
		switch {
		case length == 0:
			return strings.Join(parts, "."), true
		case length&0xc0 == 0xc0:
			if offset+1 >= len(dns) {
				return "", false
			}
			target := int(binary.BigEndian.Uint16(dns[offset:offset+2])) & 0x3fff
			// RFC 1035 compression pointers refer to a prior occurrence. This
			// also makes traversal strictly decreasing; retain a small explicit
			// hop cap so one packet cannot impose excessive repeated work.
			pointerHops++
			if target >= offset || target >= len(dns) || pointerHops > maxDNSCompressionPointers {
				return "", false
			}
			offset = target
		case length&0xc0 != 0 || length > 63 || offset+1+length > len(dns):
			return "", false
		default:
			expandedLength += length
			if len(parts) > 0 {
				expandedLength++
			}
			if expandedLength > 253 {
				return "", false
			}
			offset++
			parts = append(parts, string(dns[offset:offset+length]))
			offset += length
		}
	}
	return "", false
}
