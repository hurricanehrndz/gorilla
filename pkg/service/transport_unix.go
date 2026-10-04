//go:build !windows

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// The Unix transport is a Unix domain socket, <socketDir>/<name>.sock. It
// exists so the portable service core runs and is tested off Windows; the
// service itself is still only installed on Windows.

// socketDir is where sockets live; tests point it at a temporary directory.
var socketDir = defaultSocketDir()

func defaultSocketDir() string {
	if runtime.GOOS == "linux" {
		return "/run/gorilla"
	}
	return "/var/run/gorilla"
}

func socketPath(name string) string {
	return filepath.Join(socketDir, name+".sock")
}

type unixListener struct {
	net.Listener
}

// listen binds the socket. The directory is 0755 and the socket 0666, the
// rough equivalent of the pipe's Authenticated Users read/write.
//
// CEILING: any local user can connect, as on Windows, and the service does
// not check who did. Peer credentials (SO_PEERCRED on Linux, LOCAL_PEERCRED
// on macOS) are not read; read them in Accept once operations need an owner.
func listen(name string) (listener, error) {
	path := socketPath(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create socket directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("set socket directory mode: %w", err)
	}
	// A socket left by a service that did not shut down cleanly blocks bind.
	if info, err := os.Lstat(path); err == nil && info.Mode().Type() == fs.ModeSocket {
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove stale socket: %w", err)
		}
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o666); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("set socket mode: %w", err)
	}
	return unixListener{ln}, nil
}

func (l unixListener) Accept() (io.ReadWriteCloser, error) {
	return l.Listener.Accept()
}

func dial(ctx context.Context, name string, timeout time.Duration) (io.ReadWriteCloser, error) {
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "unix", socketPath(name))
	if err != nil && errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("service is not running: %w", err)
	}
	return conn, err
}
