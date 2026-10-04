package manifest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"go.yaml.in/yaml/v4"
)

// mkdirAll is a test seam for the self-serve save path.
var mkdirAll = os.MkdirAll

// SelfServePath returns the path to the self-serve manifest under appDataPath.
func SelfServePath(appDataPath string) string {
	return filepath.Join(appDataPath, "service-manifest.yaml")
}

// LoadSelfServe reads the self-serve manifest at path. A missing file returns a
// named empty manifest rather than an error.
func LoadSelfServe(path string) (Item, error) {
	defaultManifest := Item{
		Name:     "service-manifest",
		Installs: []string{},
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return defaultManifest, nil
		}
		return Item{}, fmt.Errorf("unable to read service local manifest %s: %w", path, err)
	}

	entry := defaultManifest
	if err := yaml.Unmarshal(data, &entry); err != nil {
		return Item{}, fmt.Errorf("unable to parse service local manifest %s: %w", path, err)
	}
	if entry.Name == "" {
		entry.Name = defaultManifest.Name
	}
	return entry, nil
}

// SaveSelfServe persists the self-serve manifest at path. It keeps the three
// self-serve lists (managed_installs, managed_uninstalls, default_installs) and
// drops includes/catalogs/updates, which the self-serve file never owns.
func SaveSelfServe(path string, entry Item) error {
	if err := mkdirAll(filepath.Clean(filepath.Dir(path)), 0o755); err != nil {
		return fmt.Errorf("unable to create local manifest directory: %w", err)
	}

	entry.Includes = nil
	entry.Catalogs = nil
	entry.Updates = nil

	data, err := yaml.Marshal(entry)
	if err != nil {
		return fmt.Errorf("unable to encode service local manifest: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("unable to write service local manifest %s: %w", path, err)
	}
	return nil
}

// selfServeMu serialises UpdateSelfServe. The service writes the self-serve
// manifest from its command queue and, for a cancel, while a managed run in the
// same process is under way, so a plain load and save could lose a write.
var selfServeMu sync.Mutex

// UpdateSelfServe loads the self-serve manifest at path, lets fn change it, and
// saves it when fn reports a change, all under one in-process lock.
// CEILING: the lock is per process. The service owns the file and runs every
// managed run in-process; a separate `gorilla` CLI run racing the service could
// still lose a write. Upgrade to a file lock if both ever write concurrently.
func UpdateSelfServe(path string, fn func(*Item) bool) error {
	selfServeMu.Lock()
	defer selfServeMu.Unlock()
	entry, err := LoadSelfServe(path)
	if err != nil {
		return err
	}
	if !fn(&entry) {
		return nil
	}
	return SaveSelfServe(path, entry)
}
