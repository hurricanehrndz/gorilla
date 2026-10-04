package process

import (
	"log/slog"
	"slices"

	"github.com/1dustindavis/gorilla/pkg/catalog"
	"github.com/1dustindavis/gorilla/pkg/manifest"
	"github.com/1dustindavis/gorilla/pkg/status"
)

// statusCheck is a test seam over the status checker, mirroring installerInstall.
var statusCheck = (*status.Checker).CheckStatus

// ReconcileSelfServe reconciles the self-serve manifest against the admin
// manifests and returns the authorized installs and the pass-through uninstalls
// to feed into the run. It mutates selfServe in place for the once-only default
// state machine and reports whether the file needs saving.
//
// Authorization set is the union of every manifest's optional_installs only. As
// in Munki, a default_installs name only asserts intent into the self-serve
// file (analyze.py:747-748); it must also be offered in optional_installs to
// actually install, since installation flows exclusively through the self-serve
// managed_installs filtered against available optional installs (core.py:264-278).
// A default-only name is recorded but filtered out of the returned installs.
func ReconcileSelfServe(selfServe *manifest.Item, manifests []manifest.Item) (installs, uninstalls []string, changed bool) {
	// 1. Defaults (R4): assert each server default exactly once. A name already
	// in the self-serve default_installs record is left alone, so a default the
	// user later removed never re-asserts.
	recorded := make(map[string]bool, len(selfServe.DefaultInstalls))
	for _, name := range selfServe.DefaultInstalls {
		recorded[name] = true
	}
	for _, m := range manifests {
		for _, name := range m.DefaultInstalls {
			if name == "" || recorded[name] {
				continue
			}
			recorded[name] = true
			selfServe.DefaultInstalls = append(selfServe.DefaultInstalls, name)
			if !slices.Contains(selfServe.Installs, name) {
				selfServe.Installs = append(selfServe.Installs, name)
			}
			changed = true
		}
	}
	if changed {
		slices.Sort(selfServe.Installs)
		slices.Sort(selfServe.DefaultInstalls)
	}

	// 2. Authorization (R2): only names the admin offers are processed as
	// installs; anything else is warned and left in the file (it may become
	// available again — we keep user intent).
	available := make(map[string]bool)
	for _, m := range manifests {
		for _, name := range m.OptionalInstalls {
			available[name] = true
		}
	}
	for _, name := range selfServe.Installs {
		if available[name] {
			installs = append(installs, name)
			continue
		}
		slog.Warn("self-serve item not in optional_installs, skipping", "item", name)
	}

	// 3. Uninstalls pass through (R2): a deselected item must be removable even
	// after it leaves the optional list. An item an admin manifest requires
	// (managed_installs) is never removed on a self-service request; the
	// service refuses such a request, and this catches one written to the file
	// some other way. It is left in the file, like an unauthorized install.
	required := make(map[string]bool)
	for _, m := range manifests {
		for _, name := range m.Installs {
			required[name] = true
		}
	}
	for _, name := range selfServe.Uninstalls {
		if required[name] {
			slog.Warn("self-serve uninstall of an item in managed_installs, skipping", "item", name)
			continue
		}
		uninstalls = append(uninstalls, name)
	}
	return installs, uninstalls, changed
}

// PruneSelfServeUninstalls drops managed_uninstalls entries whose software is
// confirmed gone so the user can reinstall later (R5). An entry that no longer
// resolves to a catalog item is kept (the catalog may return); a still-installed
// item is kept and retried next run. Returns whether the file needs saving.
func PruneSelfServeUninstalls(selfServe *manifest.Item, catalogsMap map[int]map[string]catalog.Item, checker *status.Checker, cachePath string) (changed bool) {
	kept := make([]string, 0, len(selfServe.Uninstalls))
	for _, name := range selfServe.Uninstalls {
		item, ok := firstItem(name, catalogsMap)
		if !ok {
			// firstItem already warned; keep the entry so it can be retried.
			kept = append(kept, name)
			continue
		}
		installed, err := statusCheck(checker, item, "uninstall", cachePath)
		if err != nil {
			slog.Warn("unable to check uninstall status, keeping entry", "item", name, "err", err)
			kept = append(kept, name)
			continue
		}
		if installed {
			// Still on disk (removal failed or deferred); retry next run.
			kept = append(kept, name)
			continue
		}
		changed = true
	}
	if changed {
		selfServe.Uninstalls = kept
	}
	return changed
}
