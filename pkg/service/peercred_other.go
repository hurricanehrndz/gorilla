//go:build !windows && !linux && !darwin

package service

import (
	"fmt"
	"net"
	"runtime"
)

// peerUID is not implemented here: the Unix transport only runs the tests,
// on Linux and macOS.
func peerUID(*net.UnixConn) (uint32, error) {
	return 0, fmt.Errorf("peer credentials are not supported on %s", runtime.GOOS)
}
