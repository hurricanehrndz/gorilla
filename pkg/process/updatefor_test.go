package process

import (
	"reflect"
	"testing"

	"github.com/1dustindavis/gorilla/pkg/catalog"
	"github.com/1dustindavis/gorilla/pkg/installer"
	"github.com/1dustindavis/gorilla/pkg/report"
	"github.com/1dustindavis/gorilla/pkg/status"
)

// item is a small helper for building update_for test catalogs.
func item(name string, updateFor, deps []string) catalog.Item {
	return catalog.Item{
		Name:         name,
		DisplayName:  name,
		Installer:    catalog.InstallerItem{Type: "msi", Location: name + ".msi"},
		UpdateFor:    updateFor,
		Dependencies: deps,
	}
}

// TestUpdaterIndex verifies referent->updater mapping across catalog precedence
// with first-catalog-wins dedupe.
func TestUpdaterIndex(t *testing.T) {
	catalogs := map[int]map[string]catalog.Item{
		2: {
			"AppExtra": item("AppExtra", []string{"App"}, nil),
			"AppPatch": item("AppPatch", []string{"App"}, nil), // dup of cat 1
		},
		1: {
			"App":      item("App", nil, nil),
			"AppPatch": item("AppPatch", []string{"App"}, nil),
		},
	}
	got := UpdaterIndex(catalogs)
	// cat 1 contributes AppPatch first; cat 2 adds AppExtra (AppPatch deduped).
	want := map[string][]string{"App": {"AppPatch", "AppExtra"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("UpdaterIndex\nExpected: %#v\nActual: %#v", want, got)
	}
}

// TestInstallsUpdaterRidesAlong verifies an updater installs after its referent,
// with the updater's own dependencies resolved first.
func TestInstallsUpdaterRidesAlong(t *testing.T) {
	installerInstall = fakeInstall
	actualInstalledItems = nil
	defer func() {
		installerInstall = origInstall
		actualInstalledItems = nil
	}()

	catalogs := map[int]map[string]catalog.Item{1: {
		"App":       item("App", nil, nil),
		"AppUpdate": item("AppUpdate", []string{"App"}, []string{"UpdateDep"}),
		"UpdateDep": item("UpdateDep", nil, nil),
	}}
	r := &installer.Runner{Report: report.New()}
	Installs([]string{"App"}, catalogs, r, UpdaterIndex(catalogs))

	// App installs, then its updater — whose own dependency installs first.
	want := []string{"App", "UpdateDep", "AppUpdate"}
	if !reflect.DeepEqual(want, actualInstalledItems) {
		t.Fatalf("attempted items\nExpected: %#v\nActual: %#v", want, actualInstalledItems)
	}
}

// TestInstallsUpdaterCycle verifies mutually-updating items are each installed
// once (cycle caught by visited).
func TestInstallsUpdaterCycle(t *testing.T) {
	installerInstall = fakeInstall
	actualInstalledItems = nil
	defer func() {
		installerInstall = origInstall
		actualInstalledItems = nil
	}()

	catalogs := map[int]map[string]catalog.Item{1: {
		"AppA": item("AppA", []string{"AppB"}, nil),
		"AppB": item("AppB", []string{"AppA"}, nil),
	}}
	r := &installer.Runner{Report: report.New()}
	Installs([]string{"AppA"}, catalogs, r, UpdaterIndex(catalogs))

	want := []string{"AppA", "AppB"}
	if !reflect.DeepEqual(want, actualInstalledItems) {
		t.Fatalf("attempted items\nExpected: %#v\nActual: %#v", want, actualInstalledItems)
	}
}

// TestInstallsDeferredReferentSkipsUpdaters verifies a deferred referent does
// not process its updaters (Munki: dependents of skipped work are skipped).
func TestInstallsDeferredReferentSkipsUpdaters(t *testing.T) {
	installerInstall = deferOnBlocking
	actualInstalledItems = nil
	defer func() {
		installerInstall = origInstall
		actualInstalledItems = nil
	}()

	catalogs := map[int]map[string]catalog.Item{1: {
		"Blocked":       {Name: "Blocked", DisplayName: "Blocked", Installer: catalog.InstallerItem{Type: "msi", Location: "Blocked.msi"}, BlockingApps: []string{"notepad"}},
		"BlockedUpdate": item("BlockedUpdate", []string{"Blocked"}, nil),
	}}
	r := &installer.Runner{Report: report.New()}
	Installs([]string{"Blocked"}, catalogs, r, UpdaterIndex(catalogs))

	// Only the referent is attempted; its updater is skipped.
	if !reflect.DeepEqual([]string{"Blocked"}, actualInstalledItems) {
		t.Fatalf("attempted items\nExpected: %#v\nActual: %#v", []string{"Blocked"}, actualInstalledItems)
	}
	assertDeferredItems(t, r, map[string]string{"Blocked": "blocking application(s) running: notepad.exe"})
	assertFailedItems(t, r, map[string]string{})
}

// TestInstalledReferentUpdaters verifies updaters ride along for referents merely
// installed on disk, and not for uninstalled referents or referents already
// being installed this run.
func TestInstalledReferentUpdaters(t *testing.T) {
	catalogs := map[int]map[string]catalog.Item{1: {
		"Installed":         item("Installed", nil, nil),
		"InstalledUpd":      item("InstalledUpd", []string{"Installed"}, nil),
		"NotInstalled":      item("NotInstalled", nil, nil),
		"NotInstalledUpd":   item("NotInstalledUpd", []string{"NotInstalled"}, nil),
		"BeingInstalled":    item("BeingInstalled", nil, nil),
		"BeingInstalledUpd": item("BeingInstalledUpd", []string{"BeingInstalled"}, nil),
	}}

	orig := statusCheck
	defer func() { statusCheck = orig }()
	statusCheck = func(_ *status.Checker, it catalog.Item, installType, _ string) (bool, error) {
		if installType != "uninstall" {
			t.Fatalf("expected uninstall check, got %q", installType)
		}
		// Installed and BeingInstalled are on disk; NotInstalled is not.
		return it.Name != "NotInstalled", nil
	}

	installsSet := map[string]bool{"BeingInstalled": true}
	got := InstalledReferentUpdaters(catalogs, UpdaterIndex(catalogs), installsSet, &status.Checker{}, "cache")

	// Only InstalledUpd: NotInstalledUpd's referent is absent; BeingInstalledUpd's
	// referent is already being installed this run (covered by the in-walk path).
	want := []string{"InstalledUpd"}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("InstalledReferentUpdaters\nExpected: %#v\nActual: %#v", want, got)
	}
}

// TestExpandUninstallsWithUpdaters verifies transitive removal coupling and
// cycle safety.
func TestExpandUninstallsWithUpdaters(t *testing.T) {
	// A removed; U1 update_for A; U2 update_for U1 -> all three removed.
	index := map[string][]string{"A": {"U1"}, "U1": {"U2"}}
	got := ExpandUninstallsWithUpdaters([]string{"A"}, index)
	want := []string{"A", "U1", "U2"}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("transitive expansion\nExpected: %#v\nActual: %#v", want, got)
	}

	// Cycle: A<->U1 must not loop.
	cyc := map[string][]string{"A": {"U1"}, "U1": {"A"}}
	got = ExpandUninstallsWithUpdaters([]string{"A"}, cyc)
	want = []string{"A", "U1"}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("cycle expansion\nExpected: %#v\nActual: %#v", want, got)
	}
}
