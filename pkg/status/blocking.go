package status

import "strings"

// matchBlockingApps returns the subset of apps whose normalized image name is
// currently running. Both sides are lowercased and a trailing ".exe" is
// stripped, so "Notepad", "notepad.exe", and "NOTEPAD.EXE" all match a running
// "notepad.exe" (spec R6). Kept build-tag-free so it is testable on any OS; the
// Windows-only file supplies the real process snapshot.
func matchBlockingApps(apps, running []string) []string {
	runningSet := make(map[string]struct{}, len(running))
	for _, name := range running {
		runningSet[normalizeApp(name)] = struct{}{}
	}
	var blocking []string
	for _, app := range apps {
		if _, ok := runningSet[normalizeApp(app)]; ok {
			blocking = append(blocking, app)
		}
	}
	return blocking
}

func normalizeApp(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".exe")
}
