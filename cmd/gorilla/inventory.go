package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/1dustindavis/gorilla/pkg/catalog"
	"github.com/1dustindavis/gorilla/pkg/config"
	"github.com/1dustindavis/gorilla/pkg/manifest"
	"github.com/1dustindavis/gorilla/pkg/process"
	"github.com/1dustindavis/gorilla/pkg/report"
	"github.com/1dustindavis/gorilla/pkg/status"
	"github.com/1dustindavis/gorilla/pkg/version"
)

// inventoryFile is written under cfg.AppDataPath (ProgramData\gorilla).
const inventoryFile = "inventory.json"

// legacyReportFile is the pre-inventory report, GorillaReport.json. It is kept
// only so a run can delete the stale copy an upgraded machine still has (its
// ACL is the inherited, user-readable one). Nothing writes it.
const legacyReportFile = "GorillaReport.json"

// planBuilder collects every item the run considers, tagged with its kind, in
// the order the run meets them. The first kind recorded for a name wins.
type planBuilder struct {
	catalogs map[int]map[string]catalog.Item
	// requestedBy names the user behind each self-service request this run
	// carries out (cfg.RequestedBy).
	requestedBy map[string]string
	items       []report.PlanItem
	seen        map[string]bool
	warnings    []string
}

func newPlanBuilder() *planBuilder {
	return &planBuilder{seen: make(map[string]bool)}
}

// add records names under kind, resolving display name and version from the
// catalog item the run acts on. Removals and updates that resolve to no valid
// catalog item are skipped by the run without a record, so, as Munki does,
// they are left out with a warning rather than reported pending forever.
// Install-side items stay: the install walk records their failure.
func (b *planBuilder) add(kind string, selfService bool, names ...string) {
	for _, name := range names {
		if name == "" || b.seen[name] {
			continue
		}
		b.seen[name] = true
		item, ok := process.ResolveItem(name, b.catalogs)
		if !ok && (kind == report.KindManagedUninstall || kind == report.KindManagedUpdate) {
			b.warnings = append(b.warnings, fmt.Sprintf("%s %s skipped: not found or invalid in any catalog", kind, name))
			continue
		}
		b.items = append(b.items, report.PlanItem{
			Name:        name,
			DisplayName: item.DisplayName,
			Version:     item.Version,
			Kind:        kind,
			SelfService: selfService,
			RequestedBy: b.requestedFor(name, selfService),
		})
	}
}

// requestedFor is the user who asked for name, for a self-service item.
func (b *planBuilder) requestedFor(name string, selfService bool) string {
	if !selfService {
		return ""
	}
	return b.requestedBy[name]
}

// addSelfServe records the self-serve installs, telling defaults apart from
// user-chosen optional installs.
func (b *planBuilder) addSelfServe(installs []string, selfServe manifest.Item) {
	for _, name := range installs {
		kind := report.KindOptionalInstall
		if slices.Contains(selfServe.DefaultInstalls, name) {
			kind = report.KindDefaultInstall
		}
		b.add(kind, true, name)
	}
}

// addUpdaters records the update_for updaters of every item the install walk
// visits, transitively, matching the walk's in-run expansion. Updates and
// uninstalls do not expand in-walk, so they are skipped.
func (b *planBuilder) addUpdaters(index map[string][]string) {
	for i := 0; i < len(b.items); i++ {
		if kind := b.items[i].Kind; kind == report.KindManagedUninstall || kind == report.KindManagedUpdate {
			continue
		}
		b.add(report.KindUpdateFor, false, index[b.items[i].Name]...)
	}
}

// addAvailable records offered optional installs the user has not selected,
// with a real install check under the ListOptionalInstalls rules: script-only
// checks are not run and report not installed.
// CEILING: an unselected optional that only has a script check always reads
// installed:false, even when it is present. Upgrade: run the check script here
// (the run already executes scripts as SYSTEM), and settle ListOptionalInstalls
// OQ-C4 the same way so the two surfaces agree.
func (b *planBuilder) addAvailable(manifests []manifest.Item, checker *status.Checker, cachePath string) {
	for _, m := range manifests {
		for _, name := range m.OptionalInstalls {
			if name == "" || b.seen[name] {
				continue
			}
			b.add(report.KindOptionalInstall, false, name)
			item, ok := process.ResolveItem(name, b.catalogs)
			if !ok || scriptOnlyCheck(item) {
				continue
			}
			installed, err := checker.CheckStatus(item, "uninstall", cachePath)
			if err != nil {
				slog.Warn("unable to check optional item status", "item", name, "err", err)
				continue
			}
			b.items[len(b.items)-1].Installed = installed
		}
	}
}

func scriptOnlyCheck(item catalog.Item) bool {
	return item.Check.Script != "" && item.Check.File == nil &&
		item.Check.Registry.Version == "" && item.Check.Appx.Name == ""
}

// checkUpdates sets Installed on managed_update items the run found nothing to
// do for. The update check answers "no action" both when the item is current
// and when it is absent, and Munki leaves absent managed_updates out of the
// report, so the inventory needs to know which one it was.
func (b *planBuilder) checkUpdates(run *report.Report, checker *status.Checker, cachePath string) {
	for i := range b.items {
		pi := &b.items[i]
		if pi.Kind != report.KindManagedUpdate ||
			!slices.ContainsFunc(run.NoActionItems, func(n report.NoActionItem) bool { return n.Name == pi.Name }) {
			continue
		}
		item, ok := process.ResolveItem(pi.Name, b.catalogs)
		if !ok {
			continue
		}
		installed, err := checker.CheckStatus(item, "uninstall", cachePath)
		if err != nil {
			slog.Warn("unable to check managed update status", "item", pi.Name, "err", err)
			continue
		}
		pi.Installed = installed
	}
}

// finishInventory builds the run's inventory and, for a real run, writes it
// as inventory.json; check-only prints it instead. It runs even when the run
// failed, recording the failure in Errors. A write failure is logged, never
// returned: the run's own outcome stands.
func finishInventory(cfg config.Configuration, run *report.Report, b *planBuilder, start time.Time, runErr error) {
	plan := report.Plan{
		ConsoleUser:           consoleUser(),
		ManifestName:          cfg.Manifest,
		ManagedInstallVersion: version.Version().Version,
		StartTime:             start,
		EndTime:               time.Now(),
		CheckOnly:             cfg.CheckOnly,
		Warnings:              b.warnings,
		Items:                 b.items,
	}
	if runErr != nil {
		plan.Errors = append(plan.Errors, runErr.Error())
	}
	inv := run.Inventory(plan)

	if cfg.CheckOnly {
		data, err := json.MarshalIndent(inv, "", "  ")
		if err != nil {
			slog.Warn("Unable to encode inventory", "err", err)
			return
		}
		fmt.Println(string(data))
		return
	}

	path := filepath.Join(cfg.AppDataPath, inventoryFile)
	slog.Info("Saving inventory", "path", path)
	if err := report.WriteInventory(path, inv); err != nil {
		slog.Warn("Unable to save inventory", "path", path, "err", err)
		return
	}
	removeLegacyReport(cfg.AppDataPath)
}

// removeLegacyReport deletes the stale legacy report once the inventory has
// replaced it. os.Remove on a reparse point removes the link, not its target.
// Failure never fails the run.
func removeLegacyReport(dir string) {
	path := filepath.Join(dir, legacyReportFile)
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		slog.Debug("Unable to remove legacy report", "path", path, "err", err)
	}
}
