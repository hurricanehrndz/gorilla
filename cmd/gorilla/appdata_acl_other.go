//go:build !windows

package main

// protectAppData is a no-op off Windows, where Gorilla only runs for
// development.
func protectAppData(string) error { return nil }
