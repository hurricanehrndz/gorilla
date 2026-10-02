//go:build !windows

package main

// consoleUser has no console session to inspect off Windows.
func consoleUser() string { return "" }
