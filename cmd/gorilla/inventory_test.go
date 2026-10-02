package main

import (
	"strings"
	"testing"

	"github.com/1dustindavis/gorilla/pkg/catalog"
	"github.com/1dustindavis/gorilla/pkg/report"
)

// TestPlanBuilderKinds: the kind decides how osquery reads an item's status
// (a removal reads "removed", not "pending"), so the first kind a name gets
// must stick, and only items the install walk visits pull in their updaters.
// Versions must come from the catalog item the run acts on, and a removal the
// run cannot resolve is a warning, not a forever-pending item.
func TestPlanBuilderKinds(t *testing.T) {
	valid := catalog.InstallerItem{Type: "ps1", Location: "packages/x.ps1"}
	b := newPlanBuilder()
	b.catalogs = map[int]map[string]catalog.Item{
		1: {"App": {Name: "App", DisplayName: "Invalid Entry", Version: "0.9"}},
		2: {
			"App":       {Name: "App", DisplayName: "The App", Version: "1.0", Installer: valid},
			"Tool":      {Name: "Tool", Installer: valid},
			"AppPatch":  {Name: "AppPatch", Installer: valid},
			"OldPatch":  {Name: "OldPatch", Installer: valid},
			"ToolPatch": {Name: "ToolPatch", Installer: valid},
		},
	}
	b.add(report.KindManagedInstall, false, "App")
	b.add(report.KindManagedUninstall, false, "Old", "App")
	b.add(report.KindManagedUpdate, false, "Tool")
	b.addUpdaters(map[string][]string{
		"App":  {"AppPatch"},
		"Old":  {"OldPatch"},
		"Tool": {"ToolPatch"},
	})

	got := make(map[string]report.PlanItem)
	for _, pi := range b.items {
		got[pi.Name] = pi
	}
	if app := got["App"]; app.Kind != report.KindManagedInstall || app.DisplayName != "The App" || app.Version != "1.0" {
		t.Errorf("App = %#v, want first kind and the valid catalog entry", app)
	}
	if got["AppPatch"].Kind != report.KindUpdateFor {
		t.Errorf("AppPatch = %#v, want update_for", got["AppPatch"])
	}
	for _, name := range []string{"Old", "OldPatch", "ToolPatch"} {
		if _, ok := got[name]; ok {
			t.Errorf("%s planned, want it left out", name)
		}
	}
	if len(b.warnings) != 1 || !strings.Contains(b.warnings[0], "Old") {
		t.Errorf("expected one warning for the unresolvable removal, got %v", b.warnings)
	}
}
