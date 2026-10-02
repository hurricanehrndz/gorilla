//go:build !windows

package report

// protectFile is a no-op off Windows, where Gorilla only runs for development.
func protectFile(string) error { return nil }
