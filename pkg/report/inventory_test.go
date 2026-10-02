package report

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/1dustindavis/gorilla/pkg/catalog"
)

// findItem returns the inventory item named name, failing the test if absent.
func findItem(t *testing.T, items []InventoryItem, name string) InventoryItem {
	t.Helper()
	for _, it := range items {
		if it.Name == name {
			return it
		}
	}
	t.Fatalf("item %q not found in %#v", name, items)
	return InventoryItem{}
}

// TestInventoryFailedInstallIsProblem: a failed install must surface as a
// problem in osquery, never as installed, or fleet queries hide the failure.
func TestInventoryFailedInstallIsProblem(t *testing.T) {
	r := New()
	r.FailedItems = append(r.FailedItems, FailedItem{Name: "Chrome", Version: "1.0", Action: "install", Error: "exit status 1603"})

	inv := r.Inventory(Plan{Items: []PlanItem{{Name: "Chrome", Version: "1.0", Kind: KindManagedInstall}}})

	it := findItem(t, inv.ManagedInstalls, "Chrome")
	if it.Status != StatusFailed || it.Installed || it.InstalledVersion != "" || it.Error != "exit status 1603" {
		t.Errorf("failed install reported as %#v", it)
	}
	findItem(t, inv.ProblemInstalls, "Chrome")
	for _, name := range inv.InstalledItems {
		if name == "Chrome" {
			t.Errorf("failed install listed in InstalledItems: %v", inv.InstalledItems)
		}
	}
	if len(inv.Errors) != 1 {
		t.Errorf("expected one error for the failure, got %v", inv.Errors)
	}
}

// TestInventoryDeferredCarriesReason: a blocking-app deferral is not a
// failure, and the reason tells an admin which app held it up.
func TestInventoryDeferredCarriesReason(t *testing.T) {
	reason := "blocking application(s) running: notepad.exe"
	r := New()
	r.DeferredItems = append(r.DeferredItems, DeferredItem{Name: "DemoBlocked", Version: "1.0", Action: "install", Reason: reason})

	inv := r.Inventory(Plan{Items: []PlanItem{{Name: "DemoBlocked", Version: "1.0", Kind: KindOptionalInstall, SelfService: true}}})

	it := findItem(t, inv.ManagedInstalls, "DemoBlocked")
	if it.Status != StatusDeferred || it.DeferredReason != reason || it.Installed {
		t.Errorf("deferred item reported as %#v", it)
	}
	if len(inv.ProblemInstalls) != 0 {
		t.Errorf("deferral counted as a problem: %#v", inv.ProblemInstalls)
	}
	findItem(t, inv.ItemsToInstall, "DemoBlocked")
}

// TestInventoryUnselectedOptionalIsAvailable: offered-but-unselected optional
// items are part of the fleet inventory, with their real install state.
func TestInventoryUnselectedOptionalIsAvailable(t *testing.T) {
	inv := New().Inventory(Plan{Items: []PlanItem{
		{Name: "Offered", Version: "2.0", Kind: KindOptionalInstall},
		{Name: "OfferedPresent", Version: "3.0", Kind: KindOptionalInstall, Installed: true},
	}})

	if it := findItem(t, inv.ManagedInstalls, "Offered"); it.Status != StatusAvailable || it.Installed || it.SelfService {
		t.Errorf("unselected optional reported as %#v", it)
	}
	if it := findItem(t, inv.ManagedInstalls, "OfferedPresent"); it.Status != StatusAvailable || !it.Installed || it.InstalledVersion != "3.0" {
		t.Errorf("unselected installed optional reported as %#v", it)
	}
	if len(inv.ItemsToInstall) != 0 {
		t.Errorf("available items must not be queued: %#v", inv.ItemsToInstall)
	}
}

// TestInventoryStatusFromRunResults covers the remaining status mappings,
// including the NoActionItems record that makes "already installed" honest.
func TestInventoryStatusFromRunResults(t *testing.T) {
	r := New()
	r.InstalledItems = append(r.InstalledItems, catalog.Item{Name: "Fresh", Version: "1.0"})
	r.UninstalledItems = append(r.UninstalledItems, catalog.Item{Name: "Gone", Version: "1.0"})
	r.NoActionItems = append(
		r.NoActionItems,
		NoActionItem{Name: "Current", Version: "4.0", Action: "install"},
		NoActionItem{Name: "Absent", Version: "1.0", Action: "uninstall"},
	)
	plan := Plan{Items: []PlanItem{
		{Name: "Fresh", Version: "1.0", Kind: KindManagedInstall},
		{Name: "Gone", Version: "1.0", Kind: KindManagedUninstall},
		{Name: "Current", Version: "4.0", Kind: KindManagedUpdate},
		{Name: "Absent", Version: "1.0", Kind: KindManagedUninstall},
		{Name: "NotReached", Version: "1.0", Kind: KindManagedInstall},
	}}
	inv := r.Inventory(plan)

	want := map[string]struct {
		status    string
		installed bool
	}{
		"Fresh":      {StatusInstalled, true},
		"Gone":       {StatusRemoved, false},
		"Current":    {StatusInstalled, true},
		"Absent":     {StatusRemoved, false},
		"NotReached": {StatusPending, false},
	}
	for name, w := range want {
		it := findItem(t, inv.ManagedInstalls, name)
		if it.Status != w.status || it.Installed != w.installed {
			t.Errorf("%s: got status %q installed %v, want %q %v", name, it.Status, it.Installed, w.status, w.installed)
		}
	}
	if got := findItem(t, inv.ManagedInstalls, "Current").InstalledVersion; got != "4.0" {
		t.Errorf("no-action install should report installed_version 4.0, got %q", got)
	}

	// Check-only records would-act items as InstalledItems; they are pending.
	plan.CheckOnly = true
	if it := findItem(t, r.Inventory(plan).ManagedInstalls, "Fresh"); it.Status != StatusPending || it.Installed {
		t.Errorf("check-only item reported as %#v", it)
	}
}

// Copies of macadmins/osquery-extension tables/munki/munki.go structs with
// json tags in place of plist tags. This is the contract for a future
// gorilla_* extension table: if this stops decoding, that table breaks.
type munkiReport struct {
	ConsoleUser           string
	StartTime             string
	EndTime               string
	Errors                []string
	Warnings              []string
	ProblemInstalls       []managedInstall
	ManagedInstallVersion string
	ManifestName          string
	ManagedInstalls       []managedInstall
}

type managedInstall struct {
	Installed        bool   `json:"installed"`
	InstalledVersion string `json:"installed_version"`
	VersionToInstall string `json:"version_to_install"`
	Name             string `json:"name"`
	DisplayName      string `json:"display_name"`
}

func TestInventoryDecodesAsMunkiReport(t *testing.T) {
	loc := time.FixedZone("MST", -7*3600)
	start := time.Date(2026, 10, 1, 9, 30, 0, 0, loc)
	r := New()
	r.InstalledItems = append(r.InstalledItems, catalog.Item{Name: "Chrome", DisplayName: "Google Chrome", Version: "1.0"})
	r.FailedItems = append(r.FailedItems, FailedItem{Name: "Broken", Version: "2.0", Action: "install", Error: "boom"})
	inv := r.Inventory(Plan{
		ConsoleUser:           "alice",
		ManifestName:          "site_default",
		ManagedInstallVersion: "3.1.0",
		StartTime:             start,
		EndTime:               start.Add(time.Minute),
		Items: []PlanItem{
			{Name: "Chrome", DisplayName: "Google Chrome", Version: "1.0", Kind: KindManagedInstall},
			{Name: "Broken", Version: "2.0", Kind: KindManagedInstall},
		},
	})

	data, err := json.Marshal(inv)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got munkiReport
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("inventory does not decode as a munki report: %v", err)
	}

	// The extension parses Munki 6 string dates with this layout.
	if parsed, err := time.Parse("2006-01-02 15:04:05 -0700", got.StartTime); err != nil || !parsed.Equal(start) {
		t.Errorf("StartTime %q: parsed %v, err %v", got.StartTime, parsed, err)
	}
	if got.EndTime != "2026-10-01 09:31:00 -0700" {
		t.Errorf("EndTime = %q", got.EndTime)
	}
	if got.ConsoleUser != "alice" || got.ManifestName != "site_default" || got.ManagedInstallVersion != "3.1.0" {
		t.Errorf("run metadata lost: %#v", got)
	}
	wantChrome := managedInstall{Installed: true, InstalledVersion: "1.0", VersionToInstall: "1.0", Name: "Chrome", DisplayName: "Google Chrome"}
	if len(got.ManagedInstalls) != 2 || got.ManagedInstalls[0] != wantChrome {
		t.Errorf("ManagedInstalls = %#v", got.ManagedInstalls)
	}
	if len(got.ProblemInstalls) != 1 || got.ProblemInstalls[0].Name != "Broken" || got.ProblemInstalls[0].Installed {
		t.Errorf("ProblemInstalls = %#v", got.ProblemInstalls)
	}
	if len(got.Errors) != 1 || got.Warnings == nil {
		t.Errorf("Errors = %#v, Warnings = %#v", got.Errors, got.Warnings)
	}
}
