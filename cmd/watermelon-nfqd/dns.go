package main

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// parseDNSResponse extracts IP->domain mappings from a raw DNS response packet.
// The packet starts at the IP header.
func parseDNSResponse(payload []byte) map[string]string {
	result := make(map[string]string)

	if len(payload) < 20 {
		return result
	}

	ihl := int(payload[0]&0x0f) * 4
	if len(payload) < ihl+8 {
		return result
	}
	// UDP header is 8 bytes; DNS starts after
	dns := payload[ihl+8:]
	if len(dns) < 12 {
		return result
	}

	// DNS header: QDCOUNT at 4-5; ANCOUNT at 6-7
	anCount := int(binary.BigEndian.Uint16(dns[6:8]))
	if anCount == 0 {
		return result
	}

	// Skip questions section
	offset := 12
	qdCount := int(binary.BigEndian.Uint16(dns[4:6]))
	for i := 0; i < qdCount && offset < len(dns); i++ {
		offset = skipDNSName(dns, offset)
		if offset < 0 || offset+4 > len(dns) {
			return result
		}
		offset += 4 // QTYPE + QCLASS
	}

	// Parse answers
	for i := 0; i < anCount && offset < len(dns); i++ {
		name := readDNSName(dns, offset)
		offset = skipDNSName(dns, offset)
		if offset < 0 || offset+10 > len(dns) {
			return result
		}
		qtype := binary.BigEndian.Uint16(dns[offset : offset+2])
		rdLength := int(binary.BigEndian.Uint16(dns[offset+8 : offset+10]))
		offset += 10
		if offset+rdLength > len(dns) {
			return result
		}
		if qtype == 1 && rdLength == 4 { // A record
			ip := fmt.Sprintf("%d.%d.%d.%d", dns[offset], dns[offset+1], dns[offset+2], dns[offset+3])
			if name != "" {
				result[ip] = name
			}
		}
		offset += rdLength
	}

	return result
}

func skipDNSName(dns []byte, offset int) int {
	for offset < len(dns) {
		length := int(dns[offset])
		if length == 0 {
			return offset + 1
		}
		if length&0xC0 == 0xC0 { // pointer
			return offset + 2
		}
		offset += 1 + length
	}
	return -1
}

func readDNSName(dns []byte, offset int) string {
	var parts []string
	seen := make(map[int]bool)
	for offset < len(dns) {
		if seen[offset] {
			return ""
		}
		seen[offset] = true
		length := int(dns[offset])
		if length == 0 {
			break
		}
		if length&0xC0 == 0xC0 { // pointer
			if offset+1 >= len(dns) {
				return ""
			}
			ptr := int(binary.BigEndian.Uint16(dns[offset:offset+2])) & 0x3FFF
			offset = ptr
			continue
		}
		offset++
		if offset+length > len(dns) {
			return ""
		}
		parts = append(parts, string(dns[offset:offset+length]))
		offset += length
	}
	return strings.Join(parts, ".")
}
