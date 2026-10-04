//go:build windows

package service

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

var testPipeSeq atomic.Uint64

// testPipeName is a fresh pipe name. The test serves it as itself, not as
// SYSTEM, so its clients trust a pipe the test's user owns. It counts rather
// than reading the clock: the Windows wall clock moves in timer ticks, and a
// listener closed while no Accept waits keeps its spare instance until the
// process exits, so a reused name fails listen.
func testPipeName(t *testing.T) string {
	t.Helper()
	original := trustedPipeOwner
	trustedPipeOwner = func() (*windows.SID, error) {
		user, err := windows.GetCurrentProcessToken().GetTokenUser()
		if err != nil {
			return nil, err
		}
		return user.User.Sid, nil
	}
	t.Cleanup(func() { trustedPipeOwner = original })
	return fmt.Sprintf("gorilla-test-%d-%d", os.Getpid(), testPipeSeq.Add(1))
}

// distrustTestServer makes clients expect the real service's owner,
// LocalSystem, which the test's own pipe does not have.
func distrustTestServer(t *testing.T) {
	t.Helper()
	original := trustedPipeOwner
	trustedPipeOwner = func() (*windows.SID, error) {
		return windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	}
	t.Cleanup(func() { trustedPipeOwner = original })
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

// The pipe's security descriptor is the service's access control: SYSTEM and
// Administrators in full, Authenticated Users read and write but not
// FILE_CREATE_PIPE_INSTANCE (so no user can add a server instance), nothing
// inherited, and the service's own user as owner, which clients check.
func TestNamedPipeSecurityDescriptor(t *testing.T) {
	handle, err := createNamedPipe(servicePipePath(testPipeName(t)), true)
	if err != nil {
		t.Fatalf("create pipe: %v", err)
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	sd, err := windows.GetSecurityInfo(handle, windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("read security descriptor: %v", err)
	}
	if sddl := sd.String(); !strings.Contains(sddl, "D:P") {
		t.Fatalf("DACL is not protected: %s", sddl)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		t.Fatal(err)
	}
	me, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if !owner.Equals(me.User.Sid) {
		t.Fatalf("owner = %s, want the creating user %s", owner, me.User.Sid)
	}

	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	masks := make(map[string]windows.ACCESS_MASK)
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatal(err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			t.Fatalf("ACE %d is not an allow entry", i)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		masks[sid.String()] = ace.Mask
	}
	if len(masks) != 3 {
		t.Fatalf("DACL has %d entries, want 3: %v", len(masks), masks)
	}
	if masks["S-1-5-18"] != windows.GENERIC_ALL && masks["S-1-5-18"] != 0x1f01ff /* FILE_ALL_ACCESS */ {
		t.Fatalf("SYSTEM mask %#x", masks["S-1-5-18"])
	}
	au, ok := masks["S-1-5-11"]
	if !ok {
		t.Fatalf("no Authenticated Users entry: %v", masks)
	}
	if au&windows.FILE_APPEND_DATA != 0 {
		t.Fatalf("Authenticated Users may create pipe instances (mask %#x)", au)
	}
	if au&(windows.FILE_READ_DATA|windows.FILE_WRITE_DATA|windows.READ_CONTROL) != windows.FILE_READ_DATA|windows.FILE_WRITE_DATA|windows.READ_CONTROL {
		t.Fatalf("Authenticated Users cannot read, write and read the owner (mask %#x)", au)
	}
}

// The service refuses to start beside a process that already has an instance
// of its pipe name, instead of quietly becoming a second server.
func TestListenFailsWhenThePipeNameIsTaken(t *testing.T) {
	name := testPipeName(t)
	squatter, err := createNamedPipe(servicePipePath(name), false)
	if err != nil {
		t.Fatalf("create the squatting instance: %v", err)
	}
	defer func() { _ = windows.CloseHandle(squatter) }()

	if ln, err := listen(name); err == nil || !strings.Contains(err.Error(), "already exists") {
		if ln != nil {
			_ = ln.Close()
		}
		t.Fatalf("listen on a taken name = %v, want an already-exists error", err)
	}
}
