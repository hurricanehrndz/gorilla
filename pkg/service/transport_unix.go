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
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
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
// rough equivalent of the pipe's Authenticated Users read/write: any local
// user can connect, and the peer credentials name who did.
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

func (l unixListener) Accept() (clientConn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return unixConn{c.(*net.UnixConn)}, nil
}

// unixConn is an accepted socket connection.
type unixConn struct {
	*net.UnixConn
}

// Abort fails blocked and later I/O on the connection.
func (c unixConn) Abort() {
	_ = c.SetDeadline(time.Now())
}

// Peer is the user of the connecting process, from its peer credentials.
func (c unixConn) Peer() (peer, error) {
	uid, err := peerUID(c.UnixConn)
	if err != nil {
		return peer{}, fmt.Errorf("read peer credentials: %w", err)
	}
	id := strconv.FormatUint(uint64(uid), 10)
	u, err := user.LookupId(id)
	if err != nil {
		return peer{ID: id}, fmt.Errorf("look up uid %s: %w", id, err)
	}
	return peer{Name: u.Username, ID: id}, nil
}

// trustedServerUID is the user the service runs as: root. Tests, which serve
// the socket as themselves, trust their own uid instead.
var trustedServerUID uint32

// dial connects to the service and checks, from the socket's peer
// credentials, that it runs as trustedServerUID before anything is sent.
func dial(ctx context.Context, name string, timeout time.Duration) (io.ReadWriteCloser, error) {
	path := socketPath(name)
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("service is not running: %w", err)
		}
		return nil, err
	}
	uid, err := peerUID(conn.(*net.UnixConn))
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("read the server's credentials on %s: %w", path, err)
	}
	if uid != trustedServerUID {
		_ = conn.Close()
		return nil, fmt.Errorf("refusing %s: it is served by uid %d, not %d, so it is not the Gorilla service", path, uid, trustedServerUID)
	}
	return conn, nil
}
