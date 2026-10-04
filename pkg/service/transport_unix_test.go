//go:build !windows

package service

import (
	"os"
	"path/filepath"
	"testing"
)

// testPipeName points the socket directory at a fresh temporary directory for
// the test. os.MkdirTemp keeps the path short: a socket path is limited to
// about 104 bytes. The test serves the socket as itself, not as root, so its
// clients trust its own uid.
func testPipeName(t *testing.T) string {
	t.Helper()
	originalUID := trustedServerUID
	trustedServerUID = uint32(os.Getuid())
	t.Cleanup(func() { trustedServerUID = originalUID })
	dir, err := os.MkdirTemp("", "gorilla")
	if err != nil {
		t.Fatal(err)
	}
	original := socketDir
	socketDir = filepath.Join(dir, "run")
	t.Cleanup(func() {
		socketDir = original
		_ = os.RemoveAll(dir)
	})
	return "gorilla-test"
}

// distrustTestServer makes clients expect a uid the test's server is not.
func distrustTestServer(t *testing.T) {
	t.Helper()
	original := trustedServerUID
	trustedServerUID = uint32(os.Getuid()) + 1
	t.Cleanup(func() { trustedServerUID = original })
}

// The socket is the Unix stand-in for the pipe's DACL: any local user may
// connect (0666) and only root may replace it (0755 directory). A socket left
// behind by an unclean exit does not stop the next start.
func TestListenSocketModesAndStaleSocket(t *testing.T) {
	name := testPipeName(t)
	if err := os.MkdirAll(socketDir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale, err := listen(name)
	if err != nil {
		t.Fatalf("first listen: %v", err)
	}
	// Leave the socket file behind, as a crash would.
	stale.(unixListener).Listener.(interface{ SetUnlinkOnClose(bool) }).SetUnlinkOnClose(false)
	_ = stale.Close()

	ln, err := listen(name)
	if err != nil {
		t.Fatalf("listen over a stale socket: %v", err)
	}
	defer func() { _ = ln.Close() }()

	for path, want := range map[string]os.FileMode{socketDir: 0o755, socketPath(name): 0o666} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode = %o, want %o", path, got, want)
		}
	}
}
