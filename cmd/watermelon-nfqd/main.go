//go:build linux

// watermelon-nfqd is the NFQUEUE interceptor daemon that runs inside the Linux VM.
// It intercepts TCP SYN packets, performs reverse DNS lookups, and consults the
// host-side verdict server to decide whether to allow or block each connection.
package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	nfqueue "github.com/florianl/go-nfqueue/v2"
	"github.com/saeta-eth/watermelon/internal/ask"
)

func main() {
	serverAddr := flag.String("server", "", "verdict server address (host:port)")
	flag.Parse()

	if *serverAddr == "" {
		fmt.Fprintln(os.Stderr, "usage: watermelon-nfqd -server HOST:PORT")
		os.Exit(1)
	}

	// Wait for verdict server to be reachable
	for {
		conn, err := net.DialTimeout("tcp", *serverAddr, 2*time.Second)
		if err == nil {
			conn.Close()
			break
		}
		log.Printf("waiting for verdict server at %s...", *serverAddr)
		time.Sleep(time.Second)
	}

	log.Printf("verdict server reachable at %s", *serverAddr)

	var cache sync.Map
	var dnsCache sync.Map // IP string → domain string

	config := nfqueue.Config{
		NfQueue:      0,
		MaxPacketLen: 128,
		MaxQueueLen:  256,
		Copymode:     nfqueue.NfQnlCopyPacket,
	}

	nf, err := nfqueue.Open(&config)
	if err != nil {
		log.Fatalf("open nfqueue: %v", err)
	}
	defer nf.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hookFunc := func(a nfqueue.Attribute) int {
		if a.Payload == nil || len(*a.Payload) < 20 {
			if a.PacketID != nil {
				nf.SetVerdict(*a.PacketID, nfqueue.NfDrop)
			}
			return 0
		}

		payload := *a.Payload
		dstIP := net.IPv4(payload[16], payload[17], payload[18], payload[19])

		dstPort := 0
		ihl := int(payload[0]&0x0f) * 4
		if len(payload) >= ihl+4 {
			dstPort = int(binary.BigEndian.Uint16(payload[ihl+2 : ihl+4]))
		}

		srcPort := 0
		if len(payload) >= ihl+2 {
			srcPort = int(binary.BigEndian.Uint16(payload[ihl : ihl+2]))
		}

		ipStr := dstIP.String()

		// Look up domain from DNS snooping cache (preferred) or fall back to reverse DNS
		domain := ipStr
		if d, ok := dnsCache.Load(ipStr); ok {
			domain = d.(string)
		} else {
			names, lookupErr := net.LookupAddr(ipStr)
			if lookupErr == nil && len(names) > 0 {
				domain = names[0]
				if len(domain) > 0 && domain[len(domain)-1] == '.' {
					domain = domain[:len(domain)-1]
				}
			}
		}

		// Cache by domain (not IP) so shared-IP domains get independent verdicts
		cacheKey := fmt.Sprintf("%s:%d", domain, dstPort)
		if v, ok := cache.Load(cacheKey); ok {
			verdict := v.(string)
			if verdict == ask.VerdictBlock {
				nf.SetVerdict(*a.PacketID, nfqueue.NfDrop)
			} else {
				nf.SetVerdict(*a.PacketID, nfqueue.NfAccept)
			}
			return 0
		}

		process := resolveProcess(srcPort)

		verdict := askServer(*serverAddr, ask.VerdictRequest{
			Domain:  domain,
			Port:    dstPort,
			Process: process,
			IP:      ipStr,
		})

		// Only cache block and always-allow; allow-once should re-prompt
		if verdict != ask.VerdictAllowOnce {
			cache.Store(cacheKey, verdict)
		}

		if verdict == ask.VerdictBlock {
			nf.SetVerdict(*a.PacketID, nfqueue.NfDrop)
		} else {
			nf.SetVerdict(*a.PacketID, nfqueue.NfAccept)
		}
		return 0
	}

	errFunc := func(e error) int {
		log.Printf("nfqueue error: %v", e)
		return 0
	}

	err = nf.RegisterWithErrorFunc(ctx, hookFunc, errFunc)
	if err != nil {
		log.Fatalf("register handler: %v", err)
	}

	// DNS snooping queue (queue 1) — intercept DNS responses to build IP→domain map
	dnsConfig := nfqueue.Config{
		NfQueue:      1,
		MaxPacketLen: 512,
		MaxQueueLen:  256,
		Copymode:     nfqueue.NfQnlCopyPacket,
	}
	dnsNf, err := nfqueue.Open(&dnsConfig)
	if err != nil {
		log.Fatalf("open dns nfqueue: %v", err)
	}
	defer dnsNf.Close()

	dnsHook := func(a nfqueue.Attribute) int {
		if a.Payload == nil || len(*a.Payload) < 28 {
			if a.PacketID != nil {
				dnsNf.SetVerdict(*a.PacketID, nfqueue.NfAccept)
			}
			return 0
		}
		mappings := parseDNSResponse(*a.Payload)
		for ip, domain := range mappings {
			dnsCache.Store(ip, domain)
		}
		if a.PacketID != nil {
			dnsNf.SetVerdict(*a.PacketID, nfqueue.NfAccept)
		}
		return 0
	}
	err = dnsNf.RegisterWithErrorFunc(ctx, dnsHook, errFunc)
	if err != nil {
		log.Fatalf("register dns handler: %v", err)
	}

	log.Println("watermelon-nfqd running, intercepting TCP SYN packets...")

	// Block until SIGINT or SIGTERM
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("shutting down...")
	cancel()
}

// resolveProcess attempts to find the process name that owns the TCP connection
// with the given source port by reading /proc/net/tcp.
func resolveProcess(srcPort int) string {
	data, err := os.ReadFile("/proc/net/tcp")
	if err != nil {
		return ""
	}

	// /proc/net/tcp format: sl local_address rem_address st ... inode
	// local_address is hex IP:PORT
	hexPort := fmt.Sprintf("%04X", srcPort)
	var inode string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		localAddr := fields[1]
		parts := strings.Split(localAddr, ":")
		if len(parts) == 2 && parts[1] == hexPort {
			inode = fields[9]
			break
		}
	}
	if inode == "" || inode == "0" {
		return ""
	}

	// Search /proc/*/fd/* for socket with matching inode
	socketLink := fmt.Sprintf("socket:[%s]", inode)
	procs, err := os.ReadDir("/proc")
	if err != nil {
		return ""
	}
	for _, p := range procs {
		if !p.IsDir() {
			continue
		}
		pid := p.Name()
		if pid[0] < '0' || pid[0] > '9' {
			continue
		}
		fdDir := fmt.Sprintf("/proc/%s/fd", pid)
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			link, err := os.Readlink(fmt.Sprintf("%s/%s", fdDir, fd.Name()))
			if err != nil {
				continue
			}
			if link == socketLink {
				comm, err := os.ReadFile(fmt.Sprintf("/proc/%s/comm", pid))
				if err != nil {
					return ""
				}
				return strings.TrimSpace(string(comm))
			}
		}
	}

	return ""
}

func askServer(addr string, req ask.VerdictRequest) string {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		log.Printf("verdict server unreachable: %v (blocking)", err)
		return ask.VerdictBlock
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		log.Printf("failed to send request: %v (blocking)", err)
		return ask.VerdictBlock
	}

	var resp ask.VerdictResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		log.Printf("failed to read response: %v (blocking)", err)
		return ask.VerdictBlock
	}

	return resp.Verdict
}
