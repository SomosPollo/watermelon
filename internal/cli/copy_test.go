package cli

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestValidateCopyArgs(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		dst     string
		wantErr string
	}{
		{name: "host to vm", src: "./file.txt", dst: "myvm:/tmp/"},
		{name: "vm to host", src: "myvm:/tmp/out.log", dst: "./"},
		{name: "absolute host to vm", src: "/home/user/file.txt", dst: "myvm:/tmp/"},
		{name: "colon in explicit local path", src: "./report:2026", dst: "myvm:/tmp/"},
		{name: "colon in nested local path", src: "reports/report:2026", dst: "myvm:/tmp/"},
		{name: "remote path may contain colon", src: "myvm:/tmp/report:2026", dst: "./"},
		{name: "both vm paths", src: "vm1:/a", dst: "vm2:/b", wantErr: "both src and dst"},
		{name: "neither vm path", src: "./src", dst: "./dst", wantErr: "neither src nor dst"},
		{name: "empty vm prefix", src: ":/tmp/file", dst: "./", wantErr: "VM name cannot be empty"},
		{name: "invalid vm prefix", src: "bad$name:/tmp/file", dst: "./", wantErr: "invalid VM name"},
		{name: "empty remote path", src: "myvm:", dst: "./", wantErr: "remote path cannot be empty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCopyArgs(tt.src, tt.dst)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateCopyArgs(%q, %q) error = %v", tt.src, tt.dst, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateCopyArgs(%q, %q) error = %v, want error containing %q", tt.src, tt.dst, err, tt.wantErr)
			}
		})
	}
}

func TestCopyArgsVMName(t *testing.T) {
	for _, test := range []struct {
		src, dst string
		want     string
	}{
		{src: "./file", dst: "build-vm:/tmp/file", want: "build-vm"},
		{src: "build-vm:/tmp/file", dst: "./file", want: "build-vm"},
	} {
		got, err := copyArgsVMName(test.src, test.dst)
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Fatalf("copyArgsVMName(%q, %q) = %q, want %q", test.src, test.dst, got, test.want)
		}
	}
}

func TestCopyHandsOffLifecycleLockWhileHoldingUsageLease(t *testing.T) {
	configureLifecycleLockTest(t)
	oldCopy := cliCopyVM
	copyStarted := make(chan struct{}, 1)
	allowCopyExit := make(chan struct{})
	var allowCopyExitOnce sync.Once
	cliCopyVM = func(src, dst string, recursive bool) error {
		if src != "./src" || dst != "copy-vm:/dst" || !recursive {
			t.Errorf("copy invocation = (%q, %q, %v)", src, dst, recursive)
		}
		copyStarted <- struct{}{}
		<-allowCopyExit
		return nil
	}
	t.Cleanup(func() {
		allowCopyExitOnce.Do(func() { close(allowCopyExit) })
		cliCopyVM = oldCopy
	})

	cmd := NewCopyCmd()
	if err := cmd.Flags().Set("recursive", "true"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.RunE(cmd, []string{"./src", "copy-vm:/dst"}) }()
	select {
	case <-copyStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("copy did not reach the mocked Lima operation")
	}

	type lifecycleResult struct {
		lock *vmLifecycleLock
		err  error
	}
	lifecycleDone := make(chan lifecycleResult, 1)
	go func() {
		lock, err := acquireVMLifecycleLock("copy-vm")
		lifecycleDone <- lifecycleResult{lock: lock, err: err}
	}()
	select {
	case result := <-lifecycleDone:
		if result.err != nil {
			t.Fatal(result.err)
		}
		if err := result.lock.Release(); err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("copy retained the lifecycle lock during transfer")
	}

	type leaseResult struct {
		lease *vmUsageLease
		err   error
	}
	leaseAttempted := make(chan struct{})
	leaseDone := make(chan leaseResult, 1)
	go func() {
		close(leaseAttempted)
		lease, err := acquireExclusiveVMUsageLease("copy-vm")
		leaseDone <- leaseResult{lease: lease, err: err}
	}()
	<-leaseAttempted
	select {
	case result := <-leaseDone:
		if result.lease != nil {
			_ = result.lease.Release()
		}
		t.Fatalf("exclusive usage lease completed during active copy: %v", result.err)
	case <-time.After(100 * time.Millisecond):
	}

	allowCopyExitOnce.Do(func() { close(allowCopyExit) })
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("copy error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("copy did not finish after release")
	}
	select {
	case result := <-leaseDone:
		if result.err != nil {
			t.Fatalf("exclusive usage lease after copy: %v", result.err)
		}
		if err := result.lease.Release(); err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("copy usage lease was not released")
	}
}

func TestCopyCommandRequiresExactlyTwoOperands(t *testing.T) {
	for _, args := range [][]string{nil, {"one"}, {"one", "two", "three"}} {
		cmd := NewCopyCmd()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Fatalf("copy with args %q unexpectedly succeeded", args)
		}
	}
}

func TestCopyCommandRejectsInvalidOperandGrammarBeforeHandler(t *testing.T) {
	for _, args := range [][]string{
		{"./src", "./dst"},
		{"vm-one:/src", "vm-two:/dst"},
		{"bad$name:/src", "./dst"},
	} {
		cmd := NewCopyCmd()
		handlerCalls := 0
		cmd.RunE = func(*cobra.Command, []string) error {
			handlerCalls++
			return nil
		}
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Fatalf("copy with args %q unexpectedly passed validation", args)
		}
		if handlerCalls != 0 {
			t.Fatalf("copy with args %q called handler %d times, want 0", args, handlerCalls)
		}
	}
}
