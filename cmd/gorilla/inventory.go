package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"time"

	"github.com/1dustindavis/gorilla/pkg/catalog"
	"github.com/1dustindavis/gorilla/pkg/config"
	"github.com/1dustindavis/gorilla/pkg/manifest"
	"github.com/1dustindavis/gorilla/pkg/report"
	"github.com/1dustindavis/gorilla/pkg/status"
	"github.com/1dustindavis/gorilla/pkg/version"
)

// inventoryFile is written under cfg.AppDataPath (ProgramData\gorilla).
const inventoryFile = "inventory.json"

// planBuilder collects every item the run considers, tagged with its kind, in
// the order the run meets them. The first kind recorded for a name wins.
type planBuilder struct {
	catalogs map[int]map[string]catalog.Item
	items    []report.PlanItem
	seen     map[string]bool
}

func newPlanBuilder() *planBuilder {
	return &planBuilder{seen: make(map[string]bool)}
}

// add records names under kind, resolving display name and version from the
// first catalog that has the item.
func (b *planBuilder) add(kind string, selfService bool, names ...string) {
	for _, name := range names {
		if name == "" || b.seen[name] {
			continue
		}
		b.seen[name] = true
		item, _ := b.catalogItem(name)
		b.items = append(b.items, report.PlanItem{
			Name:        name,
			DisplayName: item.DisplayName,
			Version:     item.Version,
			Kind:        kind,
			SelfService: selfService,
		})
	}
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
func (b *planBuilder) addAvailable(manifests []manifest.Item, checker *status.Checker, cachePath string) {
	for _, m := range manifests {
		for _, name := range m.OptionalInstalls {
			if name == "" || b.seen[name] {
				continue
			}
			b.add(report.KindOptionalInstall, false, name)
			item, ok := b.catalogItem(name)
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

// catalogItem returns the first-catalog-wins item for name.
func (b *planBuilder) catalogItem(name string) (catalog.Item, bool) {
	indexes := make([]int, 0, len(b.catalogs))
	for k := range b.catalogs {
		indexes = append(indexes, k)
	}
	slices.Sort(indexes)
	for _, k := range indexes {
		if item, ok := b.catalogs[k][name]; ok {
			return item, true
		}
	}
	return catalog.Item{}, false
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
	}
}
