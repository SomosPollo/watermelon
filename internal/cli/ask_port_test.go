package cli

import (
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/saeta-eth/watermelon/internal/config"
)

func TestNewAskListenerSkipsInactiveIdentityReservedPort(t *testing.T) {
	project, _, _ := setupNamedVMIdentityTest(t)
	cfg := config.NewConfig()
	cfg.Security.Enforcement = config.EnforcementAsk

	inactive, err := reserveNamedVMIdentity(project, "ask-inactive", cfg)
	if err != nil {
		t.Fatal(err)
	}
	current, err := reserveNamedVMIdentity(project, "ask-current", cfg)
	if err != nil {
		t.Fatal(err)
	}

	reservedListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	reservedPort := reservedListener.Addr().(*net.TCPAddr).Port
	if err := reservedListener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := savePortAt(inactive.Paths.VerdictPortPath, reservedPort); err != nil {
		t.Fatal(err)
	}

	listenCalls := 0
	listener, err := listenForAskVerdictsWith(current.Identity.VMName, 0, func(network, address string) (net.Listener, error) {
		listenCalls++
		if network != "tcp4" || address != "127.0.0.1:0" {
			t.Fatalf("ephemeral listen call = %q %q, want TCP4 loopback port zero", network, address)
		}
		if listenCalls == 1 {
			// Force the kernel-selected result to collide with an inactive
			// identity's durable reservation.
			return net.Listen(network, fmt.Sprintf("127.0.0.1:%d", reservedPort))
		}
		return net.Listen(network, address)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if listenCalls != 2 {
		t.Fatalf("listen calls = %d, want one rejected collision and one retry", listenCalls)
	}
	if got := listener.Addr().(*net.TCPAddr).Port; got == reservedPort {
		t.Fatalf("new ask listener reused inactive VM port %d", reservedPort)
	}
	if got := listener.Addr().(*net.TCPAddr).IP.String(); got != "127.0.0.1" {
		t.Fatalf("new ask listener address = %q, want host loopback", got)
	}

	// Rejected listeners are released after allocation; their persistent port
	// files, rather than leaked sockets, remain the reservation primitive.
	probe, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", reservedPort))
	if err != nil {
		t.Fatalf("rejected collision listener remained open: %v", err)
	}
	_ = probe.Close()
}

func TestNewAskListenerSkipsConfiguredForwardedPort(t *testing.T) {
	project, _, _ := setupNamedVMIdentityTest(t)
	cfg := config.NewConfig()
	cfg.Security.Enforcement = config.EnforcementAsk
	current, err := reserveNamedVMIdentity(project, "ask-forward", cfg)
	if err != nil {
		t.Fatal(err)
	}

	forwardedListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	forwardedPort := forwardedListener.Addr().(*net.TCPAddr).Port
	_ = forwardedListener.Close()

	listenCalls := 0
	listener, err := listenForAskVerdictsWith(current.Identity.VMName, 0, func(network, address string) (net.Listener, error) {
		listenCalls++
		if listenCalls == 1 {
			return net.Listen(network, fmt.Sprintf("127.0.0.1:%d", forwardedPort))
		}
		return net.Listen(network, address)
	}, forwardedPort)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if got := listener.Addr().(*net.TCPAddr).Port; got == forwardedPort {
		t.Fatalf("ask listener reused configured forwarded port %d", forwardedPort)
	}
	if listenCalls != 2 {
		t.Fatalf("listen calls = %d, want forwarded-port collision plus retry", listenCalls)
	}
}

func TestSavedAskListenerRejectsConfiguredForwardedPort(t *testing.T) {
	listenCalled := false
	listener, err := listenForAskVerdictsWith("ask-existing", 39285, func(string, string) (net.Listener, error) {
		listenCalled = true
		return nil, nil
	}, 39285)
	if listener != nil {
		_ = listener.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "conflicts with a configured host port forward") {
		t.Fatalf("collision error = %v, want configured-forward rejection", err)
	}
	if listenCalled {
		t.Fatal("allocator bound a known-colliding saved ask port")
	}
}

func TestConcurrentNewAskListenersAllocateAndSaveDistinctPorts(t *testing.T) {
	project, _, _ := setupNamedVMIdentityTest(t)
	cfg := config.NewConfig()
	cfg.Security.Enforcement = config.EnforcementAsk

	const count = 12
	instances := make([]namedVMInstanceIdentity, count)
	for i := range instances {
		instance, err := reserveNamedVMIdentity(project, fmt.Sprintf("ask-concurrent-%02d", i), cfg)
		if err != nil {
			t.Fatal(err)
		}
		instances[i] = instance
	}

	listeners := make([]net.Listener, count)
	ports := make([]int, count)
	errs := make([]error, count)
	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(count)
	for i := range instances {
		go func() {
			defer wait.Done()
			<-start
			listener, err := listenForAskVerdicts(instances[i].Identity.VMName, 0)
			if err != nil {
				errs[i] = err
				return
			}
			port := listener.Addr().(*net.TCPAddr).Port
			if err := savePortAt(instances[i].Paths.VerdictPortPath, port); err != nil {
				_ = listener.Close()
				errs[i] = err
				return
			}
			listeners[i] = listener
			ports[i] = port
		}()
	}
	close(start)
	wait.Wait()
	for _, listener := range listeners {
		if listener != nil {
			t.Cleanup(func() { _ = listener.Close() })
		}
	}

	seen := make(map[int]int, count)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("allocator %d error = %v", i, err)
		}
		if previous, exists := seen[ports[i]]; exists {
			t.Fatalf("allocators %d and %d both selected port %d", previous, i, ports[i])
		}
		seen[ports[i]] = i
	}

	reserved, err := reservedAskVerdictPorts("future-ask-vm")
	if err != nil {
		t.Fatal(err)
	}
	for i, port := range ports {
		if got := reserved[port]; got != instances[i].Identity.VMName {
			t.Fatalf("saved port %d reservation = %q, want %q", port, got, instances[i].Identity.VMName)
		}
	}
}

func TestNewAskPortAllocationFailsClosedOnUnsafeRegistryPort(t *testing.T) {
	project, _, _ := setupNamedVMIdentityTest(t)
	cfg := config.NewConfig()
	cfg.Security.Enforcement = config.EnforcementAsk

	unsafeIdentity, err := reserveNamedVMIdentity(project, "ask-unsafe-port", cfg)
	if err != nil {
		t.Fatal(err)
	}
	current, err := reserveNamedVMIdentity(project, "ask-safe-current", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unsafeIdentity.Paths.VerdictPortPath, []byte("39285\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unsafeIdentity.Paths.VerdictPortPath, 0644); err != nil {
		t.Fatal(err)
	}

	listenCalled := false
	listener, err := listenForAskVerdictsWith(current.Identity.VMName, 0, func(string, string) (net.Listener, error) {
		listenCalled = true
		return nil, nil
	})
	if listener != nil {
		_ = listener.Close()
		t.Fatal("allocator returned a listener despite unsafe reserved-port state")
	}
	if err == nil || !strings.Contains(err.Error(), unsafeIdentity.Identity.VMName) || !strings.Contains(err.Error(), "insecure mode") {
		t.Fatalf("allocation error = %v, want unsafe identity and mode diagnostic", err)
	}
	if listenCalled {
		t.Fatal("allocator bound a port before authenticating the complete registry")
	}
}

func TestExplicitAskPortDoesNotDependOnUnrelatedRegistry(t *testing.T) {
	project, _, _ := setupNamedVMIdentityTest(t)
	cfg := config.NewConfig()
	cfg.Security.Enforcement = config.EnforcementAsk
	unsafeIdentity, err := reserveNamedVMIdentity(project, "ask-unrelated-unsafe", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unsafeIdentity.Paths.VerdictPortPath, []byte("not-a-port\n"), 0600); err != nil {
		t.Fatal(err)
	}

	probe, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}

	listener, err := listenForAskVerdicts("ask-existing", port)
	if err != nil {
		t.Fatalf("explicit saved-port bind consulted unrelated registry: %v", err)
	}
	if got := listener.Addr().(*net.TCPAddr).IP.String(); got != "127.0.0.1" {
		t.Fatalf("explicit saved-port listener address = %q, want host loopback", got)
	}
	_ = listener.Close()
}
