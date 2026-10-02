package main

import (
	"testing"

	"github.com/1dustindavis/gorilla/pkg/catalog"
	"github.com/1dustindavis/gorilla/pkg/report"
)

// TestPlanBuilderKinds: the kind decides how osquery reads an item's status
// (a removal reads "removed", not "pending"), so the first kind a name gets
// must stick, and only items the install walk visits pull in their updaters.
func TestPlanBuilderKinds(t *testing.T) {
	b := newPlanBuilder()
	b.catalogs = map[int]map[string]catalog.Item{
		1: {"App": {Name: "App", DisplayName: "The App", Version: "1.0"}},
		2: {"App": {Name: "App", DisplayName: "Shadowed", Version: "0.9"}},
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
		t.Errorf("App = %#v, want first kind and first catalog", app)
	}
	if got["AppPatch"].Kind != report.KindUpdateFor {
		t.Errorf("AppPatch = %#v, want update_for", got["AppPatch"])
	}
	for _, name := range []string{"OldPatch", "ToolPatch"} {
		if _, ok := got[name]; ok {
			t.Errorf("%s planned, but uninstalls and updates do not expand updaters in-walk", name)
		}
	}
}
