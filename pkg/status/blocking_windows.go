//go:build windows
// +build windows

package status

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// RunningBlockingApps returns the subset of apps currently running, matched by
// process image name (case-insensitive, trailing ".exe" optional on both
// sides — spec R6). It takes a single Toolhelp32 process snapshot per call.
//
// ponytail: one snapshot per gated item — a run gating N items takes N
// snapshots. Upgrade path: cache one snapshot per run and pass it in if this
// ever shows up in a profile.
func RunningBlockingApps(apps []string) ([]string, error) {
	if len(apps) == 0 {
		return nil, nil
	}

	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	var running []string
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		running = append(running, windows.UTF16ToString(entry.ExeFile[:]))
	}
	if err != windows.ERROR_NO_MORE_FILES {
		return nil, err
	}

	return matchBlockingApps(apps, running), nil
}
