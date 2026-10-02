package report

import (
	"fmt"
	"slices"
	"time"

	"github.com/1dustindavis/gorilla/pkg/catalog"
)

// SchemaVersion is the inventory schema version. Bump it on any change that
// renames, removes, or changes the meaning of a field.
const SchemaVersion = 1

// timeFormat is Munki 6's string date format, which the macadmins osquery
// extension parses.
const timeFormat = "2006-01-02 15:04:05 -0700"

// Item kinds: where an item came from in the manifests.
const (
	KindManagedInstall   = "managed_install"
	KindManagedUninstall = "managed_uninstall"
	KindManagedUpdate    = "managed_update"
	KindOptionalInstall  = "optional_install"
	KindDefaultInstall   = "default_install"
	KindUpdateFor        = "update_for"
)

// Item statuses: what the run did, or would do, with an item.
const (
	StatusInstalled = "installed"
	StatusPending   = "pending"
	StatusFailed    = "failed"
	StatusDeferred  = "deferred"
	StatusRemoved   = "removed"
	StatusAvailable = "available"
)

// Inventory is the osquery-facing state of one managed run. It mirrors Munki's
// ManagedInstallReport keys so a gorilla_* table can reuse the macadmins munki
// table's structs with JSON in place of plist.
type Inventory struct {
	SchemaVersion         int             `json:"schema_version"`
	ConsoleUser           string          `json:"ConsoleUser"`
	StartTime             string          `json:"StartTime"`
	EndTime               string          `json:"EndTime"`
	ManagedInstallVersion string          `json:"ManagedInstallVersion"`
	ManifestName          string          `json:"ManifestName"`
	Errors                []string        `json:"Errors"`
	Warnings              []string        `json:"Warnings"`
	ProblemInstalls       []InventoryItem `json:"ProblemInstalls"`
	ManagedInstalls       []InventoryItem `json:"ManagedInstalls"`
	InstalledItems        []string        `json:"InstalledItems"`
	RemovedItems          []string        `json:"RemovedItems"`
	ItemsToInstall        []InventoryItem `json:"ItemsToInstall"`
	ItemsToRemove         []InventoryItem `json:"ItemsToRemove"`
}

// InventoryItem is one item in the inventory. The first five keys are Munki's;
// the rest are Gorilla extras.
type InventoryItem struct {
	Name             string `json:"name"`
	DisplayName      string `json:"display_name"`
	Installed        bool   `json:"installed"`
	InstalledVersion string `json:"installed_version"`
	VersionToInstall string `json:"version_to_install"`
	Kind             string `json:"kind"`
	Status           string `json:"status"`
	SelfService      bool   `json:"self_service"`
	DeferredReason   string `json:"deferred_reason,omitempty"`
	Error            string `json:"error,omitempty"`
}

// PlanItem is one item the run considered, as the caller resolved it from the
// manifests and catalogs.
type PlanItem struct {
	Name        string
	DisplayName string
	Version     string
	Kind        string
	SelfService bool
	// Installed is the caller's own status check. It is used only for items
	// the run neither acted on nor checked: unselected optional installs.
	Installed bool
}

// Plan is the caller's view of the run: its metadata and every item it
// considered. Items are listed in order; the first entry for a name wins.
type Plan struct {
	ConsoleUser           string
	ManifestName          string
	ManagedInstallVersion string
	StartTime             time.Time
	EndTime               time.Time
	CheckOnly             bool
	Errors                []string
	Warnings              []string
	Items                 []PlanItem
}

// Inventory builds the inventory from the plan and the run's real results.
func (r *Report) Inventory(p Plan) Inventory {
	inv := Inventory{
		SchemaVersion:         SchemaVersion,
		ConsoleUser:           p.ConsoleUser,
		StartTime:             formatTime(p.StartTime),
		EndTime:               formatTime(p.EndTime),
		ManagedInstallVersion: p.ManagedInstallVersion,
		ManifestName:          p.ManifestName,
		Errors:                append([]string{}, p.Errors...),
		Warnings:              append([]string{}, p.Warnings...),
		ProblemInstalls:       []InventoryItem{},
		ManagedInstalls:       []InventoryItem{},
		InstalledItems:        []string{},
		RemovedItems:          []string{},
		ItemsToInstall:        []InventoryItem{},
		ItemsToRemove:         []InventoryItem{},
	}
	for _, f := range r.FailedItems {
		inv.Errors = append(inv.Errors, fmt.Sprintf("%s of %s failed: %s", f.Action, f.Name, f.Error))
	}

	seen := make(map[string]bool)
	for _, pi := range append(slices.Clone(p.Items), r.unplannedItems(p.Items)...) {
		if seen[pi.Name] {
			continue
		}
		seen[pi.Name] = true

		it := r.inventoryItem(pi, p.CheckOnly)
		inv.ManagedInstalls = append(inv.ManagedInstalls, it)

		removal := it.Kind == KindManagedUninstall
		switch {
		case removal && !it.Installed:
			inv.RemovedItems = append(inv.RemovedItems, it.Name)
		case !removal && it.Installed:
			inv.InstalledItems = append(inv.InstalledItems, it.Name)
		}
		if it.Status == StatusFailed && !removal && !it.Installed {
			inv.ProblemInstalls = append(inv.ProblemInstalls, it)
		}
		if it.Status == StatusPending || it.Status == StatusDeferred {
			if removal {
				inv.ItemsToRemove = append(inv.ItemsToRemove, it)
			} else {
				inv.ItemsToInstall = append(inv.ItemsToInstall, it)
			}
		}
	}
	return inv
}

// inventoryItem resolves one item's status from the run's results, most
// decisive first.
func (r *Report) inventoryItem(pi PlanItem, checkOnly bool) InventoryItem {
	it := InventoryItem{
		Name:             pi.Name,
		DisplayName:      pi.DisplayName,
		VersionToInstall: pi.Version,
		Kind:             pi.Kind,
		SelfService:      pi.SelfService,
	}
	if it.DisplayName == "" {
		it.DisplayName = pi.Name
	}
	removal := pi.Kind == KindManagedUninstall
	acted := slices.ContainsFunc(r.InstalledItems, func(i catalog.Item) bool { return i.Name == pi.Name })

	// installed marks the item present on disk at the catalog version.
	// CEILING: Gorilla's checks answer present/absent, not which version, so
	// installed_version is the catalog version. Upgrade: return the detected
	// version from status.Checker and record it here.
	installed := func() {
		it.Installed = true
		it.InstalledVersion = pi.Version
	}

	if i := slices.IndexFunc(r.FailedItems, func(f FailedItem) bool { return f.Name == pi.Name }); i >= 0 {
		it.Status = StatusFailed
		it.Error = r.FailedItems[i].Error
		// A failed removal leaves the item in place; a failed post-install
		// script follows a real install.
		if removal || (acted && !checkOnly) {
			installed()
		}
		return it
	}
	if i := slices.IndexFunc(r.DeferredItems, func(d DeferredItem) bool { return d.Name == pi.Name }); i >= 0 {
		it.Status = StatusDeferred
		it.DeferredReason = r.DeferredItems[i].Reason
		if removal {
			installed()
		}
		return it
	}
	if acted {
		// Check-only records every item it would act on as installed.
		if checkOnly {
			it.Status = StatusPending
			if removal {
				installed()
			}
			return it
		}
		it.Status = StatusInstalled
		installed()
		return it
	}
	if slices.ContainsFunc(r.UninstalledItems, func(i catalog.Item) bool { return i.Name == pi.Name }) {
		it.Status = StatusRemoved
		return it
	}
	if slices.ContainsFunc(r.NoActionItems, func(n NoActionItem) bool { return n.Name == pi.Name }) {
		if removal {
			it.Status = StatusRemoved
		} else {
			it.Status = StatusInstalled
			installed()
		}
		return it
	}
	if pi.Kind == KindOptionalInstall && !pi.SelfService {
		it.Status = StatusAvailable
		if pi.Installed {
			installed()
		}
		return it
	}
	// Not reached this run (e.g. the run stopped early).
	it.Status = StatusPending
	return it
}

// unplannedItems returns items the run acted on that the plan does not list:
// dependencies pulled in by the walk. Munki processes `requires` items into
// managed_installs, so they are reported as managed installs.
func (r *Report) unplannedItems(plan []PlanItem) []PlanItem {
	planned := make(map[string]bool, len(plan))
	for _, pi := range plan {
		planned[pi.Name] = true
	}
	var extra []PlanItem
	add := func(name, displayName, version string) {
		if name == "" || planned[name] {
			return
		}
		planned[name] = true
		extra = append(extra, PlanItem{Name: name, DisplayName: displayName, Version: version, Kind: KindManagedInstall})
	}
	for _, i := range r.InstalledItems {
		add(i.Name, i.DisplayName, i.Version)
	}
	for _, f := range r.FailedItems {
		add(f.Name, "", f.Version)
	}
	for _, d := range r.DeferredItems {
		add(d.Name, "", d.Version)
	}
	for _, n := range r.NoActionItems {
		add(n.Name, "", n.Version)
	}
	return extra
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(timeFormat)
}
