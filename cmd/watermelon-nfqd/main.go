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

		ipStr := dstIP.String()

		// Reverse DNS lookup FIRST (before cache check)
		domain := ipStr
		names, err := net.LookupAddr(ipStr)
		if err == nil && len(names) > 0 {
			domain = names[0]
			// Strip trailing dot from FQDN
			if len(domain) > 0 && domain[len(domain)-1] == '.' {
				domain = domain[:len(domain)-1]
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

		verdict := askServer(*serverAddr, ask.VerdictRequest{
			Domain: domain,
			Port:   dstPort,
			IP:     ipStr,
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

	log.Println("watermelon-nfqd running, intercepting TCP SYN packets...")

	// Block until SIGINT or SIGTERM
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("shutting down...")
	cancel()
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
