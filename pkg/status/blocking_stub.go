// Without a Windows build, the Toolhelp32 snapshot is unavailable; the gate
// proceeds as if nothing is running (matches the registry_stub.go pattern).

//go:build !windows
// +build !windows

package status

// RunningBlockingApps has no process snapshot off Windows.
func RunningBlockingApps(apps []string) ([]string, error) {
	return nil, nil
}
