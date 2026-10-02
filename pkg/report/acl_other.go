//go:build !windows

package report

import "os"

// createProtectedTemp is a plain temp file off Windows, where Gorilla only
// runs for development.
func createProtectedTemp(dir, prefix string) (*os.File, error) {
	return os.CreateTemp(dir, prefix+"*.tmp")
}
