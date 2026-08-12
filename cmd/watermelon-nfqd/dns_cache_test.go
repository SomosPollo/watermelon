package main

import (
	"testing"
	"time"
)

func TestDNSAttributionCacheExpiresAndRefreshesMappings(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cache := newDNSAttributionCache(10, time.Hour)
	cache.now = func() time.Time { return now }
	mapping := dnsMapping{IP: "192.0.2.10", Domain: "EXAMPLE.test.", TTL: time.Minute}

	cache.Observe([]dnsMapping{mapping})
	if got := cache.Destination(mapping.IP); got != "example.test" {
		t.Fatalf("initial destination = %q, want normalized hostname", got)
	}

	now = now.Add(50 * time.Second)
	cache.Observe([]dnsMapping{mapping}) // refresh the same IP/domain pair
	now = now.Add(20 * time.Second)
	if got := cache.Destination(mapping.IP); got != "example.test" {
		t.Fatalf("refreshed destination = %q, want live hostname", got)
	}

	now = now.Add(41 * time.Second)
	if got := cache.Destination(mapping.IP); got != mapping.IP {
		t.Fatalf("expired destination = %q, want direct-IP fallback", got)
	}
}

func TestDNSAttributionCacheSharedIPIsAmbiguous(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cache := newDNSAttributionCache(10, time.Hour)
	cache.now = func() time.Time { return now }
	ip := "192.0.2.20"

	cache.Observe([]dnsMapping{
		{IP: ip, Domain: "one.example", TTL: time.Minute},
		{IP: ip, Domain: "two.example", TTL: 10 * time.Second},
	})
	if got := cache.Destination(ip); got != ip {
		t.Fatalf("shared-IP destination = %q, want IP because attribution is ambiguous", got)
	}

	now = now.Add(11 * time.Second)
	if got := cache.Destination(ip); got != "one.example" {
		t.Fatalf("destination after shorter mapping expired = %q, want sole live hostname", got)
	}
}

func TestDNSAttributionCacheZeroTTLRemovesMapping(t *testing.T) {
	cache := newDNSAttributionCache(10, time.Hour)
	ip := "192.0.2.30"
	cache.Observe([]dnsMapping{{IP: ip, Domain: "gone.example", TTL: time.Minute}})
	cache.Observe([]dnsMapping{{IP: ip, Domain: "gone.example", TTL: 0}})
	if got := cache.Destination(ip); got != ip {
		t.Fatalf("zero-TTL destination = %q, want direct-IP fallback", got)
	}
}

func TestDNSAttributionCacheCapsTTLAndEntryCount(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cache := newDNSAttributionCache(2, time.Minute)
	cache.now = func() time.Time { return now }
	cache.Observe([]dnsMapping{
		{IP: "192.0.2.1", Domain: "oldest.example", TTL: 24 * time.Hour},
		{IP: "192.0.2.2", Domain: "middle.example", TTL: 24 * time.Hour},
		{IP: "192.0.2.3", Domain: "newest.example", TTL: 24 * time.Hour},
	})

	if cache.entryCount != 2 {
		t.Fatalf("entry count = %d, want hard cap of 2", cache.entryCount)
	}
	if got := cache.Destination("192.0.2.1"); got != "192.0.2.1" {
		t.Fatalf("oldest entry was not evicted: got %q", got)
	}
	if got := cache.Destination("192.0.2.3"); got != "newest.example" {
		t.Fatalf("newest entry = %q, want hostname", got)
	}

	now = now.Add(time.Minute)
	if got := cache.Destination("192.0.2.3"); got != "192.0.2.3" {
		t.Fatalf("max-TTL-capped entry = %q, want expired IP fallback", got)
	}
}

func TestDNSAttributionCacheCapacityNeverMakesSharedIPLookUnique(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cache := newDNSAttributionCache(2, time.Hour)
	cache.now = func() time.Time { return now }
	sharedIP := "192.0.2.10"
	cache.Observe([]dnsMapping{
		{IP: sharedIP, Domain: "one.example", TTL: time.Minute},
		{IP: sharedIP, Domain: "two.example", TTL: time.Minute},
	})

	cache.Observe([]dnsMapping{{IP: "192.0.2.20", Domain: "new.example", TTL: time.Minute}})
	if got := cache.Destination(sharedIP); got != sharedIP {
		t.Fatalf("evicted shared-IP destination = %q, want IP rather than a surviving alias", got)
	}
	if got := cache.Destination("192.0.2.20"); got != "new.example" {
		t.Fatalf("new destination = %q, want hostname", got)
	}
}

func TestDNSAttributionCacheSameIPOverflowCollapsesToBoundedAmbiguity(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cache := newDNSAttributionCache(1, time.Hour)
	cache.now = func() time.Time { return now }
	ip := "192.0.2.30"
	cache.Observe([]dnsMapping{{IP: ip, Domain: "one.example", TTL: time.Minute}})
	cache.Observe([]dnsMapping{{IP: ip, Domain: "two.example", TTL: 10 * time.Second}})

	if got := cache.Destination(ip); got != ip {
		t.Fatalf("overflowed shared-IP destination = %q, want IP", got)
	}
	if cache.entryCount != 1 || len(cache.ambiguous) != 1 {
		t.Fatalf("collapsed cache counts = entries %d, ambiguity buckets %d; want 1/1", cache.entryCount, len(cache.ambiguous))
	}
	now = now.Add(11 * time.Second)
	if got := cache.Destination(ip); got != ip {
		t.Fatalf("collapsed destination before longest TTL expires = %q, want IP", got)
	}
	now = now.Add(50 * time.Second)
	if got := cache.Destination(ip); got != ip {
		t.Fatalf("expired collapsed destination = %q, want IP with no stale attribution", got)
	}
	if cache.entryCount != 0 || len(cache.ambiguous) != 0 {
		t.Fatalf("expired cache counts = entries %d, ambiguity buckets %d; want 0/0", cache.entryCount, len(cache.ambiguous))
	}
}

func TestDNSAttributionCacheAmbiguitySentinelsShareGlobalBound(t *testing.T) {
	cache := newDNSAttributionCache(2, time.Hour)
	for _, ip := range []string{"192.0.2.1", "192.0.2.2", "192.0.2.3"} {
		cache.Observe([]dnsMapping{
			{IP: ip, Domain: "one.example", TTL: time.Hour},
			{IP: ip, Domain: "two.example", TTL: time.Hour},
			{IP: ip, Domain: "three.example", TTL: time.Hour},
		})
	}
	if cache.entryCount > 2 || len(cache.ambiguous) > 2 {
		t.Fatalf("cache exceeded global bound: count=%d ambiguity=%d", cache.entryCount, len(cache.ambiguous))
	}
}
