//go:build linux

// watermelon-nfqd is the NFQUEUE interceptor daemon that runs inside the Linux VM.
// It intercepts TCP SYN packets, correlates names observed in DNS responses,
// and consults the host-side verdict server to allow or block each connection.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	nfqueue "github.com/florianl/go-nfqueue/v2"
	"github.com/saeta-eth/watermelon/internal/ask"
	"golang.org/x/sys/unix"
)

const (
	verdictWriteTimeout     = 5 * time.Second
	verdictResponseTimeout  = 10 * time.Minute
	maxVerdictResponseBytes = 1 << 10
)

func main() {
	serverAddr := flag.String("server", "", "verdict server address (host:port)")
	authKeyFile := flag.String("auth-key-file", "", "root-owned verdict authentication key file")
	flag.Parse()

	if *serverAddr == "" || *authKeyFile == "" {
		fmt.Fprintln(os.Stderr, "usage: watermelon-nfqd -server HOST:PORT -auth-key-file PATH")
		os.Exit(1)
	}
	authKey, err := loadAuthKeyFile(*authKeyFile)
	if err != nil {
		log.Fatalf("load verdict authentication key: %v", err)
	}
	if err := runNFQDaemon(*serverAddr, authKey); err != nil {
		log.Fatalf("watermelon-nfqd: %v", err)
	}
}

func runNFQDaemon(serverAddr string, authKey ask.AuthKey) error {
	dnsCache := newDNSAttributionCache(defaultDNSCacheEntries, defaultDNSCacheMaxTTL)

	config := nfqueue.Config{
		NfQueue:      0,
		MaxPacketLen: 128,
		MaxQueueLen:  256,
		Copymode:     nfqueue.NfQnlCopyPacket,
	}

	nf, err := nfqueue.Open(&config)
	if err != nil {
		return fmt.Errorf("open TCP nfqueue: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		_ = nf.Close()
	}()

	tcpHandler := newTCPPacketHandler(
		nf,
		dnsCache,
		func(req ask.VerdictRequest) (string, bool) { return askServer(serverAddr, authKey, req) },
		resolveProcess,
	)

	fatalQueueErrors := make(chan error, 2)
	tcpErrFunc := func(err error) int {
		return handleNFQueueReceiveError("TCP", fatalQueueErrors, err)
	}

	err = nf.RegisterWithErrorFunc(ctx, tcpHandler.Handle, tcpErrFunc)
	if err != nil {
		return fmt.Errorf("register TCP nfqueue handler: %w", err)
	}

	// DNS snooping queue (queue 1) — intercept DNS responses to build IP→domain map
	dnsConfig := nfqueue.Config{
		NfQueue:      1,
		MaxPacketLen: dnsQueueMaxPacketLen,
		MaxQueueLen:  256,
		Copymode:     nfqueue.NfQnlCopyPacket,
	}
	dnsNf, err := nfqueue.Open(&dnsConfig)
	if err != nil {
		return fmt.Errorf("open DNS nfqueue: %w", err)
	}
	defer func() { _ = dnsNf.Close() }()

	dnsHook := func(a nfqueue.Attribute) int { return handleDNSPacket(dnsNf, dnsCache, a) }
	dnsErrFunc := func(err error) int {
		return handleNFQueueReceiveError("DNS", fatalQueueErrors, err)
	}
	err = dnsNf.RegisterWithErrorFunc(ctx, dnsHook, dnsErrFunc)
	if err != nil {
		return fmt.Errorf("register DNS nfqueue handler: %w", err)
	}

	log.Println("watermelon-nfqd running, intercepting TCP SYN packets...")

	// Block until SIGINT or SIGTERM
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	select {
	case <-sigCh:
		log.Println("shutting down...")
		cancel()
		return nil
	case err := <-fatalQueueErrors:
		cancel()
		return err
	}
}

// handleNFQueueReceiveError tells go-nfqueue to continue only when the error is
// explicitly retryable. Permanent receive failures stop that queue and are
// propagated to main so systemd observes a non-zero daemon exit and restarts it.
func handleNFQueueReceiveError(queueName string, fatalErrors chan<- error, err error) int {
	var netErr net.Error
	if errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary()) {
		log.Printf("temporary %s nfqueue receive error: %v", queueName, err)
		return 0
	}

	fatalErr := fmt.Errorf("%s nfqueue receive failed: %w", queueName, err)
	select {
	case fatalErrors <- fatalErr:
	default:
	}
	return 1
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

// askServer returns the verdict and whether it came from an authenticated host
// response. Callers fail closed on false but must not cache that transient
// transport/authentication failure as though it were an explicit user block.
func askServer(addr string, authKey ask.AuthKey, req ask.VerdictRequest) (string, bool) {
	if err := ask.AuthenticateRequest(authKey, &req); err != nil {
		log.Printf("failed to authenticate verdict request: %v (blocking)", err)
		return ask.VerdictBlock, false
	}

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		log.Printf("verdict server unreachable: %v (blocking)", err)
		return ask.VerdictBlock, false
	}
	defer conn.Close()

	if err := conn.SetWriteDeadline(time.Now().Add(verdictWriteTimeout)); err != nil {
		log.Printf("failed to set verdict request deadline: %v (blocking)", err)
		return ask.VerdictBlock, false
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		log.Printf("failed to send request: %v (blocking)", err)
		return ask.VerdictBlock, false
	}

	if err := conn.SetReadDeadline(time.Now().Add(verdictResponseTimeout)); err != nil {
		log.Printf("failed to set verdict response deadline: %v (blocking)", err)
		return ask.VerdictBlock, false
	}
	resp, err := decodeVerdictResponse(conn)
	if err != nil {
		log.Printf("failed to read response: %v (blocking)", err)
		return ask.VerdictBlock, false
	}
	if !ask.VerifyResponse(authKey, req, resp) {
		log.Printf("verdict server returned an unauthenticated or invalid response (blocking)")
		return ask.VerdictBlock, false
	}

	return resp.Verdict, true
}

func decodeVerdictResponse(conn net.Conn) (ask.VerdictResponse, error) {
	reader := bufio.NewReaderSize(conn, maxVerdictResponseBytes+1)
	line, isPrefix, err := reader.ReadLine()
	if err != nil {
		return ask.VerdictResponse{}, err
	}
	if isPrefix || len(line) > maxVerdictResponseBytes {
		return ask.VerdictResponse{}, errors.New("verdict response exceeds size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	var resp ask.VerdictResponse
	if err := decoder.Decode(&resp); err != nil {
		return ask.VerdictResponse{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return ask.VerdictResponse{}, errors.New("verdict response contains trailing JSON")
		}
		return ask.VerdictResponse{}, err
	}
	return resp, nil
}

func loadAuthKeyFile(path string) (ask.AuthKey, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return ask.AuthKey{}, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return ask.AuthKey{}, errors.New("invalid authentication key file descriptor")
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return ask.AuthKey{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || !ok {
		return ask.AuthKey{}, errors.New("authentication key must be a regular file")
	}
	if stat.Uid != 0 {
		return ask.AuthKey{}, errors.New("authentication key must be owned by root")
	}
	if stat.Mode&07777 != 0600 {
		return ask.AuthKey{}, fmt.Errorf("authentication key has mode %04o; want 0600", stat.Mode&07777)
	}
	data, err := io.ReadAll(io.LimitReader(file, ask.AuthKeyBytes*2+1))
	if err != nil {
		return ask.AuthKey{}, err
	}
	if len(data) != ask.AuthKeyBytes*2 {
		return ask.AuthKey{}, errors.New("authentication key file has invalid length")
	}
	return ask.ParseAuthKey(string(data))
}
