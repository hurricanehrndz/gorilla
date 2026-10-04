//go:build windows

package service

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func testPipeName(*testing.T) string {
	return fmt.Sprintf("gorilla-test-%d", time.Now().UnixNano())
}

func TestFlushAndDisconnectNamedPipeStillDisconnectsWhenFlushReportsBrokenPipe(t *testing.T) {
	var calls []string

	originalFlush := flushNamedPipeBuffers
	originalDisconnect := disconnectNamedPipe
	t.Cleanup(func() {
		flushNamedPipeBuffers = originalFlush
		disconnectNamedPipe = originalDisconnect
	})

	flushNamedPipeBuffers = func(_ windows.Handle) error {
		calls = append(calls, "flush")
		return windows.ERROR_BROKEN_PIPE
	}
	disconnectNamedPipe = func(_ windows.Handle) error {
		calls = append(calls, "disconnect")
		return windows.ERROR_PIPE_NOT_CONNECTED
	}

	flushAndDisconnectNamedPipe(windows.InvalidHandle)

	if len(calls) != 2 {
		t.Fatalf("expected exactly two pipe calls, got %d (%v)", len(calls), calls)
	}
	if calls[0] != "flush" || calls[1] != "disconnect" {
		t.Fatalf("expected call order flush -> disconnect, got %v", calls)
	}
}

// The pipe's DACL is the service's only access control: SYSTEM and
// Administrators, plus read/write for Authenticated Users, nothing inherited.
func TestNamedPipeSecurityDescriptor(t *testing.T) {
	handle, err := createNamedPipe(servicePipePath(testPipeName(t)))
	if err != nil {
		t.Fatalf("create pipe: %v", err)
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	sd, err := windows.GetSecurityInfo(handle, windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("read security descriptor: %v", err)
	}
	sddl := sd.String()
	if !strings.HasPrefix(sddl, "D:P") {
		t.Fatalf("DACL is not protected: %s", sddl)
	}
	if got := strings.Count(sddl, "(A;"); got != 3 {
		t.Fatalf("DACL has %d allow entries, want 3: %s", got, sddl)
	}
	for _, trustee := range []string{";;;SY)", ";;;BA)", ";;;AU)"} {
		if !strings.Contains(sddl, trustee) {
			t.Fatalf("DACL lacks %s: %s", trustee, sddl)
		}
	}
}
