package process

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/1dustindavis/gorilla/pkg/catalog"
	"github.com/1dustindavis/gorilla/pkg/installer"
	"github.com/1dustindavis/gorilla/pkg/manifest"
	"github.com/1dustindavis/gorilla/pkg/report"
)

// firstItem returns the first valid occurrence of an item in a map of catalogs.
// It logs warnings for invalid/missing items and returns false when no valid item is found.
func firstItem(itemName string, catalogsMap map[int]map[string]catalog.Item) (catalog.Item, bool) {
	// Get the keys in the map and sort them so we can loop over them in order
	keys := make([]int, 0)
	for k := range catalogsMap {
		keys = append(keys, k)
	}
	sort.Ints(keys)

	var invalidReasons []string

	// loop through each catalog and return if we find a match
	for _, k := range keys {
		// If
		if item, exists := catalogsMap[k][itemName]; exists {
			// If it does exist, we should confirm it is a valid item
			validInstallItem := (item.Installer.Type != "" && item.Installer.Location != "")
			validUninstallItem := (item.Uninstaller.Type != "" && item.Uninstaller.Location != "") ||
				item.Uninstaller.Type == "msix" ||
				item.Installer.Type == "msix"

			if validInstallItem || validUninstallItem {
				return item, true
			}

			missing := []string{}
			if item.Installer.Type == "" {
				missing = append(missing, "installer.type")
			}
			if item.Installer.Location == "" {
				missing = append(missing, "installer.location")
			}
			if item.Uninstaller.Type == "" {
				missing = append(missing, "uninstaller.type")
			}
			if item.Uninstaller.Location == "" {
				missing = append(missing, "uninstaller.location")
			}
			invalidReasons = append(invalidReasons, fmt.Sprintf("catalog index %d missing required fields: %s", k, strings.Join(missing, ", ")))
		}
	}

	// No valid item found. Log why and continue processing other items.
	if len(invalidReasons) > 0 {
		slog.Warn(
			"skipping catalog item: missing required installer/uninstaller type/location fields",
			"item", itemName,
			"reasons", strings.Join(invalidReasons, "; "),
		)
		return catalog.Item{}, false
	}
	slog.Warn("skipping item: not found in any catalog", "item", itemName)
	return catalog.Item{}, false
}

// Manifests iterates though the first manifest and any included manifests
func Manifests(manifests []manifest.Item, catalogsMap map[int]map[string]catalog.Item) (installs, uninstalls, updates []string) {
	// Compile all of the installs, uninstalls, and updates into arrays
	for _, manifestItem := range manifests {
		// Installs
		for _, item := range manifestItem.Installs {
			// Check for the first valid item from our catalogs
			// Continue to the next item in the loop if we get an error
			if _, ok := firstItem(item, catalogsMap); !ok {
				continue
			}

			// If we didnt error, append the item to our installs list
			installs = append(installs, item)
		}
		// Uninstalls
		for _, item := range manifestItem.Uninstalls {
			// Check for the first valid item from our catalogs
			// Continue to the next item in the loop if we get an error
			if _, ok := firstItem(item, catalogsMap); !ok {
				continue
			}

			// If we didnt error, append the item to our uninstalls list
			uninstalls = append(uninstalls, item)
		}
		// Updates
		for _, item := range manifestItem.Updates {
			// Check for the first valid item from our catalogs
			// Continue to the next item in the loop if we get an error
			if _, ok := firstItem(item, catalogsMap); !ok {
				continue
			}

			// If we didnt error, append the item to our updates list
			updates = append(updates, item)
		}
	}
	return
}

// This abstraction allows us to override when testing
var installerInstall = (*installer.Runner).Install

// installOne runs a single item action and logs failures; the run continues
// past item failures (Munki semantics) — the installer already recorded the
// failure in the report.
func installOne(r *installer.Runner, item catalog.Item, installerType string) {
	if _, err := installerInstall(r, item, installerType); err != nil {
		slog.Warn("item action failed", "item", item.DisplayName, "err", err)
	}
}

// depState tracks an item's progress in the per-run dependency walk (K4)
type depState int

const (
	depInProgress depState = iota + 1
	depSucceeded
	depFailed
)

// recordFailedItem records a gating failure (cycle, missing dep, or skipped
// dependent) that the installer never saw and so never recorded itself.
func recordFailedItem(r *installer.Runner, name string, err error) {
	r.Report.FailedItems = append(r.Report.FailedItems, report.FailedItem{
		Name:   name,
		Action: "install",
		Error:  err.Error(),
	})
}

// installWithDeps installs itemName's dependencies depth-first and then the
// item itself (K4, spec R6). visited gates each item to one attempt per run
// and detects cycles; a failed, invalid, or missing dependency skips its
// dependents. Returns true if the item installed (or was already up to date).
func installWithDeps(itemName string, catalogsMap map[int]map[string]catalog.Item, r *installer.Runner, visited map[string]depState) bool {
	switch visited[itemName] {
	case depSucceeded:
		return true
	case depFailed:
		return false
	case depInProgress:
		// Cycle: the item's own frame is still on the stack, so this lookup
		// already succeeded there and silently returns the same item.
		item, _ := firstItem(itemName, catalogsMap)
		slog.Warn("dependency cycle detected, skipping item", "item", item.DisplayName)
		recordFailedItem(r, item.DisplayName, fmt.Errorf("dependency cycle detected"))
		visited[itemName] = depFailed
		return false
	}

	// firstItem logs why the item is missing or invalid
	item, ok := firstItem(itemName, catalogsMap)
	if !ok {
		recordFailedItem(r, itemName, fmt.Errorf("not found in any catalog"))
		visited[itemName] = depFailed
		return false
	}

	visited[itemName] = depInProgress
	for _, dependency := range item.Dependencies {
		if !installWithDeps(dependency, catalogsMap, r, visited) {
			if visited[itemName] == depFailed {
				// a cycle back to this item already recorded it
				return false
			}
			slog.Warn("skipping item: dependency failed", "item", item.DisplayName, "dependency", dependency)
			recordFailedItem(r, item.DisplayName, fmt.Errorf("dependency %s failed", dependency))
			visited[itemName] = depFailed
			return false
		}
	}

	// Install the item; the installer records its own command failures
	if _, err := installerInstall(r, item, "install"); err != nil {
		slog.Warn("item action failed", "item", item.DisplayName, "err", err)
		visited[itemName] = depFailed
		return false
	}
	visited[itemName] = depSucceeded
	return true
}

// Installs prepares and then installs an array of items with their
// dependencies resolved recursively (K4, spec R6)
func Installs(installs []string, catalogsMap map[int]map[string]catalog.Item, r *installer.Runner) {
	visited := make(map[string]depState)
	for _, item := range installs {
		installWithDeps(item, catalogsMap, r, visited)
	}
}

// Uninstalls prepares and then installs an array of items
func Uninstalls(uninstalls []string, catalogsMap map[int]map[string]catalog.Item, r *installer.Runner) {
	// Iterate through the uninstalls array and uninstall the item
	for _, item := range uninstalls {
		// Get the first valid item from our catalogs
		// Continue to the next item in the loop if we get an error
		validItem, ok := firstItem(item, catalogsMap)
		if !ok {
			continue
		}
		// Uninstall the item
		installOne(r, validItem, "uninstall")
	}
}

// Updates prepares and then installs an array of items
func Updates(updates []string, catalogsMap map[int]map[string]catalog.Item, r *installer.Runner) {
	// Iterate through the updates array and update the item **if it is already installed**
	for _, item := range updates {
		// Get the first valid item from our catalogs
		// Continue to the next item in the loop if we get an error
		validItem, ok := firstItem(item, catalogsMap)
		if !ok {
			continue
		}
		// Update the item
		installOne(r, validItem, "update")
	}
}

// dirEmpty returns true if the directory is empty
func dirEmpty(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	// Try to get the first item in the directory
	_, err = f.Readdir(1)

	// If the we recevie an EOF error, the dir is empty
	return err == io.EOF
}

// fileOld returns true if the file is older than
// the limit defined in the variable `days`
func fileOld(info os.FileInfo) bool {
	// Age of the file
	fileAge := time.Since(info.ModTime())

	// Our limit
	days := 5

	// Convert from days
	hours := days * 24
	ageLimit := time.Duration(hours) * time.Hour

	// If the file is older than our limit, return true
	return fileAge > ageLimit
}

// This abstraction allows us to override when testing
var osRemove = os.Remove

// CleanUp checks the age of items in the cache and removes if older than 10 days
func CleanUp(cachePath string) {
	// Clean up old files
	err := filepath.Walk(cachePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			slog.Warn("Failed to access path", "path", path, "err", err)
			return err
		}
		// If not a directory and older that our limit, delete
		if !info.IsDir() && fileOld(info) {
			slog.Info("Cleaning old cached file", "path", path)
			osRemove(path)
			return nil
		}
		return nil
	})
	if err != nil {
		slog.Warn("error walking path", "path", cachePath, "err", err)
		return
	}

	// Clean up empty directories
	err = filepath.Walk(cachePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			slog.Warn("Failed to access path", "path", path, "err", err)
			return err
		}

		// If a dir and empty, delete
		if info.IsDir() && dirEmpty(path) {
			slog.Info("Cleaning empty directory", "path", path)
			osRemove(path)
			return nil

		}
		return nil
	})
	if err != nil {
		slog.Warn("error walking path", "path", cachePath, "err", err)
		return
	}
}
