//go:build windows

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The Windows transport is a named pipe, \\.\pipe\<name>, that Authenticated
// Users can connect to but not serve. It uses synchronous handles, so nothing
// interrupts a blocked ConnectNamedPipe except a client connecting: Close
// connects to the pipe itself to wake Accept.

var (
	flushNamedPipeBuffers = windows.FlushFileBuffers
	disconnectNamedPipe   = windows.DisconnectNamedPipe
)

type pipeListener struct {
	path   string
	closed atomic.Bool
	// next is the instance Accept waits on. Accept creates the following
	// instance before it hands this one over, so while the listener is open
	// the name always has an instance and no other process can take it.
	next windows.Handle
}

// listen creates the pipe's first instance. If any process already has an
// instance of the name, that fails: a squatter cannot sit on the name and
// have the service quietly become a second server beside it.
func listen(name string) (listener, error) {
	path := servicePipePath(name)
	first, err := createNamedPipe(path, true)
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return nil, fmt.Errorf("pipe %s already exists: another process holds the name (a second Gorilla service, or one impersonating it): %w", path, err)
	}
	if err != nil {
		return nil, fmt.Errorf("create pipe %s: %w", path, err)
	}
	return &pipeListener{path: path, next: first}, nil
}

// Accept waits for a client on the current instance and returns it.
func (l *pipeListener) Accept() (clientConn, error) {
	for {
		if l.closed.Load() {
			return nil, net.ErrClosed
		}
		handle := l.next
		err := windows.ConnectNamedPipe(handle, nil)
		if l.closed.Load() {
			_ = windows.CloseHandle(handle)
			return nil, net.ErrClosed
		}
		next, createErr := createNamedPipe(l.path, false)
		if createErr != nil {
			_ = windows.CloseHandle(handle)
			l.closed.Store(true) // l.next is gone; a later Accept must not wait on it
			return nil, fmt.Errorf("create pipe: %w", createErr)
		}
		l.next = next
		if err != nil && !errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
			_ = windows.CloseHandle(handle)
			if errors.Is(err, windows.ERROR_NO_DATA) || errors.Is(err, windows.ERROR_OPERATION_ABORTED) {
				continue
			}
			return nil, fmt.Errorf("connect pipe: %w", err)
		}
		return &pipeConn{File: os.NewFile(uintptr(handle), l.path), handle: handle}, nil
	}
}

// Close makes Accept return net.ErrClosed. Closing the listening handle does
// not wake a ConnectNamedPipe blocked on it (CloseHandle itself waits until a
// client connects), so Close connects to the pipe and hangs up. A failed
// connection only means nobody is waiting, or the caller's deadline covers it.
func (l *pipeListener) Close() error {
	l.closed.Store(true)
	conn, err := openPipeContext(context.Background(), l.path, time.Second)
	if err != nil {
		slog.Debug("could not wake the pipe listener", "err", err)
		return nil
	}
	return conn.Close()
}

// pipeConn is one connected server-side pipe instance. Close flushes what the
// service wrote so the client can read it, disconnects, and closes the handle,
// once, whether the handler or a service stop gets there first.
type pipeConn struct {
	*os.File
	handle  windows.Handle
	once    sync.Once
	aborted atomic.Bool
}

var errAborted = errors.New("pipe I/O aborted")

func (c *pipeConn) Read(p []byte) (int, error) {
	if c.aborted.Load() {
		return 0, errAborted
	}
	return c.File.Read(p)
}

func (c *pipeConn) Write(p []byte) (int, error) {
	if c.aborted.Load() {
		return 0, errAborted
	}
	return c.File.Write(p)
}

// Abort fails later I/O and cancels I/O in flight. The handle is synchronous,
// so neither a deadline nor Close interrupts a blocked ReadFile or WriteFile;
// CancelIoEx does, from any thread.
func (c *pipeConn) Abort() {
	c.aborted.Store(true)
	_ = windows.CancelIoEx(c.handle, nil)
}

func (c *pipeConn) Close() error {
	err := net.ErrClosed
	c.once.Do(func() {
		flushAndDisconnectNamedPipe(c.handle)
		err = c.File.Close()
	})
	return err
}

// Peer is the user of the connecting process: the service, as SYSTEM, opens
// the client process named by the pipe and reads its token.
func (c *pipeConn) Peer() (peer, error) {
	var pid uint32
	if err := windows.GetNamedPipeClientProcessId(c.handle, &pid); err != nil {
		return peer{}, fmt.Errorf("client process id: %w", err)
	}
	proc, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return peer{}, fmt.Errorf("open client process %d: %w", pid, err)
	}
	defer func() { _ = windows.CloseHandle(proc) }()
	var token windows.Token
	if err = windows.OpenProcessToken(proc, windows.TOKEN_QUERY, &token); err != nil {
		return peer{}, fmt.Errorf("open client token: %w", err)
	}
	defer func() { _ = token.Close() }()
	tokenUser, err := token.GetTokenUser()
	if err != nil {
		return peer{}, fmt.Errorf("read client token user: %w", err)
	}
	sid := tokenUser.User.Sid
	account, domain, _, err := sid.LookupAccount("")
	if err != nil {
		return peer{ID: sid.String()}, fmt.Errorf("look up %s: %w", sid, err)
	}
	return peer{Name: domain + `\` + account, ID: sid.String()}, nil
}

func flushAndDisconnectNamedPipe(handle windows.Handle) {
	if err := flushNamedPipeBuffers(handle); err != nil &&
		!errors.Is(err, windows.ERROR_BROKEN_PIPE) &&
		!errors.Is(err, windows.ERROR_NO_DATA) {
		slog.Warn("failed to flush named pipe buffers", "err", err)
	}

	if err := disconnectNamedPipe(handle); err != nil &&
		!errors.Is(err, windows.ERROR_PIPE_NOT_CONNECTED) &&
		!errors.Is(err, windows.ERROR_BROKEN_PIPE) &&
		!errors.Is(err, windows.ERROR_NO_DATA) {
		slog.Warn("failed to disconnect named pipe", "err", err)
	}
}

// pipeDACL grants SYSTEM and Administrators full access and Authenticated
// Users read and write (0x12019b: FILE_GENERIC_READ|FILE_GENERIC_WRITE less
// FILE_CREATE_PIPE_INSTANCE), with inheritance blocked. Without that one
// right a user cannot add a server instance that clients would connect to.
const pipeDACL = "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;0x12019b;;;AU)"

// pipeSecurityDescriptor is pipeDACL with the service's own user as owner.
// As SYSTEM that is LocalSystem, an owner no ordinary user can give a pipe,
// which is what clients check before they send anything.
func pipeSecurityDescriptor() (*windows.SECURITY_DESCRIPTOR, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("read the service user: %w", err)
	}
	return windows.SecurityDescriptorFromString("O:" + user.User.Sid.String() + pipeDACL)
}

// createNamedPipe creates an instance of the pipe; first fails if the name
// already has one. Remote clients are rejected: the service is local only.
func createNamedPipe(pipePath string, first bool) (windows.Handle, error) {
	sd, err := pipeSecurityDescriptor()
	if err != nil {
		return windows.InvalidHandle, fmt.Errorf("security descriptor: %w", err)
	}

	sa := windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: sd,
		InheritHandle:      0,
	}

	name, err := windows.UTF16PtrFromString(pipePath)
	if err != nil {
		return windows.InvalidHandle, err
	}

	openMode := uint32(windows.PIPE_ACCESS_DUPLEX)
	if first {
		openMode |= windows.FILE_FLAG_FIRST_PIPE_INSTANCE
	}
	return windows.CreateNamedPipe(
		name,
		openMode,
		// Byte mode: the protocol is newline-delimited, and in message mode a
		// request longer than one read failed with ERROR_MORE_DATA.
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS,
		windows.PIPE_UNLIMITED_INSTANCES,
		64*1024,
		64*1024,
		0,
		&sa,
	)
}

// trustedPipeOwner is the owner the service's pipe must have: LocalSystem.
// Tests, which serve the pipe as themselves, trust their own user instead.
var trustedPipeOwner = func() (*windows.SID, error) {
	return windows.CreateWellKnownSid(windows.WinLocalSystemSid)
}

// dial connects to the service and checks that the pipe is the service's
// before anything is sent: a process that took the name while the service was
// down would otherwise receive requests and show the user what it likes.
func dial(ctx context.Context, name string, timeout time.Duration) (io.ReadWriteCloser, error) {
	path := servicePipePath(name)
	conn, err := openPipeContext(ctx, path, timeout)
	if err != nil {
		return nil, err
	}
	if err := verifyPipeOwner(windows.Handle(conn.Fd()), path); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

// verifyPipeOwner checks that the pipe behind a client handle is owned by
// trustedPipeOwner. Only a process running as that user can create a pipe
// with that owner, and the DACL keeps everyone else from adding an instance.
//
// The owner stands in for the server process's token: GetNamedPipeServerProcessId
// names the process, but a standard user cannot open a SYSTEM process's token.
func verifyPipeOwner(handle windows.Handle, path string) error {
	sd, err := windows.GetSecurityInfo(handle, windows.SE_KERNEL_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read the owner of %s: %w", path, err)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return fmt.Errorf("read the owner of %s: %w", path, err)
	}
	want, err := trustedPipeOwner()
	if err != nil {
		return err
	}
	if !owner.Equals(want) {
		return fmt.Errorf("refusing %s: it is owned by %s, not %s, so it is not the Gorilla service", path, accountName(owner), accountName(want))
	}
	return nil
}

// accountName is DOMAIN\user for sid, or the SID string.
func accountName(sid *windows.SID) string {
	account, domain, _, err := sid.LookupAccount("")
	if err != nil {
		return sid.String()
	}
	return domain + `\` + account
}

func servicePipePath(pipeName string) string {
	if strings.HasPrefix(pipeName, `\\.\pipe\`) {
		return pipeName
	}
	return `\\.\pipe\` + strings.TrimSpace(pipeName)
}

// openPipeContext opens the client end of the pipe, retrying while no
// instance is free (between two Accepts, or all busy) until timeout.
func openPipeContext(ctx context.Context, pipePath string, timeout time.Duration) (*os.File, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		pathPtr, err := windows.UTF16PtrFromString(pipePath)
		if err != nil {
			return nil, err
		}
		handle, err := windows.CreateFile(
			pathPtr,
			// Read and write without FILE_APPEND_DATA, which on a pipe is
			// FILE_CREATE_PIPE_INSTANCE and is not granted to users.
			windows.GENERIC_READ|windows.FILE_WRITE_DATA,
			0,
			nil,
			windows.OPEN_EXISTING,
			// The server may identify the client, never act as it.
			windows.FILE_ATTRIBUTE_NORMAL|windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IDENTIFICATION,
			0,
		)
		if err == nil {
			return os.NewFile(uintptr(handle), pipePath), nil
		}
		lastErr = err
		if !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) && !errors.Is(err, windows.ERROR_PIPE_BUSY) {
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, lastErr
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}
