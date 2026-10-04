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

// The Windows transport is a named pipe, \\.\pipe\<name>, readable and
// writable by Authenticated Users. It uses synchronous handles, so nothing
// interrupts a blocked ConnectNamedPipe except a client connecting: Close
// connects to the pipe itself to wake Accept.

var (
	flushNamedPipeBuffers = windows.FlushFileBuffers
	disconnectNamedPipe   = windows.DisconnectNamedPipe
)

type pipeListener struct {
	path   string
	closed atomic.Bool
}

func listen(name string) (listener, error) {
	return &pipeListener{path: servicePipePath(name)}, nil
}

// Accept creates a pipe instance and waits for a client to connect to it.
func (l *pipeListener) Accept() (clientConn, error) {
	for {
		if l.closed.Load() {
			return nil, net.ErrClosed
		}
		handle, err := createNamedPipe(l.path)
		if err != nil {
			return nil, fmt.Errorf("create pipe: %w", err)
		}
		err = windows.ConnectNamedPipe(handle, nil)
		if l.closed.Load() {
			_ = windows.CloseHandle(handle)
			return nil, net.ErrClosed
		}
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

// pipeSecurityDescriptor grants SYSTEM and Administrators full access and
// Authenticated Users read/write, with inheritance blocked.
const pipeSecurityDescriptor = "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GRGW;;;AU)"

func createNamedPipe(pipePath string) (windows.Handle, error) {
	sd, err := windows.SecurityDescriptorFromString(pipeSecurityDescriptor)
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

	return windows.CreateNamedPipe(
		name,
		windows.PIPE_ACCESS_DUPLEX,
		windows.PIPE_TYPE_MESSAGE|windows.PIPE_READMODE_MESSAGE|windows.PIPE_WAIT,
		windows.PIPE_UNLIMITED_INSTANCES,
		64*1024,
		64*1024,
		0,
		&sa,
	)
}

func dial(ctx context.Context, name string, timeout time.Duration) (io.ReadWriteCloser, error) {
	return openPipeContext(ctx, servicePipePath(name), timeout)
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
			windows.GENERIC_READ|windows.GENERIC_WRITE,
			0,
			nil,
			windows.OPEN_EXISTING,
			windows.FILE_ATTRIBUTE_NORMAL,
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
