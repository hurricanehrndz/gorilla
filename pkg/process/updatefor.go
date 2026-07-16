package process

import (
	"log/slog"
	"sort"

	"github.com/1dustindavis/gorilla/pkg/catalog"
	"github.com/1dustindavis/gorilla/pkg/status"
)

// UpdaterIndex maps a referent item name to the names of items that declare
// update_for it (its updaters). Catalogs are iterated in index order (sorted
// keys, like firstItem) and updater names are deduped first-catalog-wins.
//
// ponytail: plain-name matching only — Munki's version-specific updater names
// (e.g. "Firefox-60.0") are not supported. Upgrade path: when a real catalog
// needs them, parse the version suffix here and gate updaters on the resolved
// referent version.
func UpdaterIndex(catalogsMap map[int]map[string]catalog.Item) map[string][]string {
	keys := make([]int, 0, len(catalogsMap))
	for k := range catalogsMap {
		keys = append(keys, k)
	}
	sort.Ints(keys)

	index := make(map[string][]string)
	seen := make(map[string]map[string]bool) // referent -> set of updater names already added
	for _, k := range keys {
		// Sort item names within the catalog for a deterministic index.
		names := make([]string, 0, len(catalogsMap[k]))
		for name := range catalogsMap[k] {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, updater := range names {
			for _, referent := range catalogsMap[k][updater].UpdateFor {
				if seen[referent] == nil {
					seen[referent] = make(map[string]bool)
				}
				if seen[referent][updater] {
					continue
				}
				seen[referent][updater] = true
				index[referent] = append(index[referent], updater)
			}
		}
	}
	return index
}

// InstalledReferentUpdaters returns the updaters of referents that are merely
// installed on disk (not being installed this run), so they ride along as
// installs (R7). installsSet is the set of names already being installed this
// run; those referents are handled by the in-walk expansion in installWithDeps.
// Referents that do not resolve are skipped; a status-check error warns and
// skips. Script-check referents do run their check script here — the run
// context is where scripts are expected to execute.
func InstalledReferentUpdaters(catalogsMap map[int]map[string]catalog.Item, index map[string][]string, installsSet map[string]bool, checker *status.Checker, cachePath string) []string {
	referents := make([]string, 0, len(index))
	for referent := range index {
		referents = append(referents, referent)
	}
	sort.Strings(referents)

	var updaters []string
	added := make(map[string]bool)
	for _, referent := range referents {
		if installsSet[referent] {
			continue
		}
		item, ok := firstItem(referent, catalogsMap)
		if !ok {
			continue
		}
		installed, err := statusCheck(checker, item, "uninstall", cachePath)
		if err != nil {
			slog.Warn("unable to check referent install status, skipping updaters", "item", referent, "err", err)
			continue
		}
		if !installed {
			continue
		}
		for _, u := range index[referent] {
			if added[u] {
				continue
			}
			added[u] = true
			updaters = append(updaters, u)
		}
	}
	return updaters
}

// ExpandUninstallsWithUpdaters transitively appends the updaters of every
// removed item so updaters are removed with their referent (R7). Original order
// is preserved with expansions appended; a visited set makes it cycle-safe.
func ExpandUninstallsWithUpdaters(uninstalls []string, index map[string][]string) []string {
	visited := make(map[string]bool, len(uninstalls))
	result := make([]string, 0, len(uninstalls))
	queue := make([]string, 0, len(uninstalls))
	for _, name := range uninstalls {
		if !visited[name] {
			visited[name] = true
			result = append(result, name)
		}
		queue = append(queue, name)
	}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		for _, updater := range index[name] {
			if visited[updater] {
				continue
			}
			visited[updater] = true
			result = append(result, updater)
			queue = append(queue, updater)
		}
	}
	return result
}
