package service

import (
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/1dustindavis/gorilla/pkg/catalog"
	"github.com/1dustindavis/gorilla/pkg/config"
	"github.com/1dustindavis/gorilla/pkg/installer"
	"github.com/1dustindavis/gorilla/pkg/manifest"
	"github.com/1dustindavis/gorilla/pkg/report"
)

// stubOptional overrides manifestGet/catalogGet so the service authorizes the
// given names as available optional installs, restoring the originals on
// cleanup. The catalog is left empty on purpose: authorization keys off the
// offered names, and the enriched list tolerates a missing catalog entry.
func stubOptional(t *testing.T, names ...string) {
	t.Helper()
	origManifest := manifestGet
	origCatalog := catalogGet
	t.Cleanup(func() { manifestGet = origManifest; catalogGet = origCatalog })
	manifestGet = func(_ config.Configuration) ([]manifest.Item, []string, error) {
		return []manifest.Item{{Name: "base", OptionalInstalls: names}}, nil, nil
	}
	catalogGet = func(_ config.Configuration) (map[int]map[string]catalog.Item, error) {
		return map[int]map[string]catalog.Item{}, nil
	}
}

// loadManifest is a small test reader for the self-serve manifest lists.
func loadManifest(t *testing.T, cfg config.Configuration) manifest.Item {
	t.Helper()
	entry, err := loadServiceLocalManifest(cfg)
	if err != nil {
		t.Fatalf("loadServiceLocalManifest failed: %v", err)
	}
	return entry
}

func TestServiceLocalManifestAddRemove(t *testing.T) {
	cfg := config.Configuration{AppDataPath: filepath.Clean(t.TempDir())}
	stubOptional(t, "GoogleChrome", "7zip")

	for _, name := range []string{"GoogleChrome", "7zip"} {
		if _, _, err := addServiceManagedInstall(cfg, name); err != nil {
			t.Fatalf("addServiceManagedInstall failed: %v", err)
		}
	}
	if _, _, err := addServiceManagedInstall(cfg, "GoogleChrome"); err != nil {
		t.Fatalf("addServiceManagedInstall dedupe failed: %v", err)
	}

	if got := loadManifest(t, cfg).Installs; !reflect.DeepEqual(got, []string{"7zip", "GoogleChrome"}) {
		t.Fatalf("unexpected installs after add: %#v", got)
	}

	if _, err := removeServiceManagedInstall(cfg, "GoogleChrome"); err != nil {
		t.Fatalf("removeServiceManagedInstall failed: %v", err)
	}

	entry := loadManifest(t, cfg)
	if !reflect.DeepEqual(entry.Installs, []string{"7zip"}) {
		t.Fatalf("unexpected installs after remove: %#v", entry.Installs)
	}
	// Removal queues the item for uninstall (R3).
	if !reflect.DeepEqual(entry.Uninstalls, []string{"GoogleChrome"}) {
		t.Fatalf("unexpected uninstalls after remove: %#v", entry.Uninstalls)
	}
}

func TestAddServiceManagedInstallsRejectsUnauthorized(t *testing.T) {
	cfg := config.Configuration{AppDataPath: filepath.Clean(t.TempDir())}
	stubOptional(t, "GoogleChrome")

	if _, _, err := addServiceManagedInstall(cfg, "NotOptional"); err == nil {
		t.Fatalf("expected authorization error for unavailable item")
	}
	// Nothing should have been written.
	if got := loadManifest(t, cfg).Installs; len(got) != 0 {
		t.Fatalf("expected no installs written on rejection, got %#v", got)
	}
}

// Any local user can call removeItem, and the run uninstalls as SYSTEM, so a
// removal is accepted only for a self-service item, and never for one an admin
// manifest requires: that would let a user strip, say, the EDR agent.
func TestRemoveServiceManagedInstallAuthorization(t *testing.T) {
	cases := []struct {
		name    string
		before  manifest.Item
		allowed bool
	}{
		{name: "Offered", allowed: true},
		{name: "Default", allowed: true},
		{name: "Delisted", before: manifest.Item{Installs: []string{"Delisted"}}, allowed: true},
		{name: "PendingRemoval", before: manifest.Item{Uninstalls: []string{"PendingRemoval"}}, allowed: true},
		{name: "EDRAgent"},
		{name: "RequiredAndOffered", before: manifest.Item{Installs: []string{"RequiredAndOffered"}}},
		{name: "NotSelfService"},
	}
	origManifest := manifestGet
	t.Cleanup(func() { manifestGet = origManifest })
	manifestGet = func(config.Configuration) ([]manifest.Item, []string, error) {
		return []manifest.Item{
			{Name: "site", Installs: []string{"EDRAgent", "NotSelfService"}},
			{Name: "base", Installs: []string{"RequiredAndOffered"}, OptionalInstalls: []string{"Offered", "RequiredAndOffered"}, DefaultInstalls: []string{"Default"}},
		}, nil, nil
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Configuration{AppDataPath: t.TempDir()}
			if err := manifest.SaveSelfServe(serviceLocalManifestPath(cfg), tc.before); err != nil {
				t.Fatal(err)
			}
			_, err := removeServiceManagedInstall(cfg, tc.name)
			got := loadManifest(t, cfg)
			if tc.allowed {
				if err != nil || !slices.Equal(got.Uninstalls, []string{tc.name}) {
					t.Fatalf("remove = %v, uninstalls %v; want it queued", err, got.Uninstalls)
				}
				return
			}
			if !errors.Is(err, errItemNotRemovable) {
				t.Fatalf("remove error = %v, want errItemNotRemovable", err)
			}
			if !slices.Equal(got.Installs, tc.before.Installs) || len(got.Uninstalls) != 0 {
				t.Fatalf("a refused remove changed the selection: %+v", got)
			}
		})
	}
}

// TestAddCancelsPendingUninstall verifies re-selecting a removed item cancels
// its pending uninstall so the two lists stay disjoint (R3).
func TestAddCancelsPendingUninstall(t *testing.T) {
	cfg := config.Configuration{AppDataPath: filepath.Clean(t.TempDir())}
	stubOptional(t, "GoogleChrome")

	if _, _, err := addServiceManagedInstall(cfg, "GoogleChrome"); err != nil {
		t.Fatalf("add failed: %v", err)
	}
	if _, err := removeServiceManagedInstall(cfg, "GoogleChrome"); err != nil {
		t.Fatalf("remove failed: %v", err)
	}
	if got := loadManifest(t, cfg).Uninstalls; !reflect.DeepEqual(got, []string{"GoogleChrome"}) {
		t.Fatalf("expected pending uninstall, got %#v", got)
	}
	if _, _, err := addServiceManagedInstall(cfg, "GoogleChrome"); err != nil {
		t.Fatalf("re-add failed: %v", err)
	}
	entry := loadManifest(t, cfg)
	if !reflect.DeepEqual(entry.Installs, []string{"GoogleChrome"}) {
		t.Fatalf("unexpected installs after re-add: %#v", entry.Installs)
	}
	if len(entry.Uninstalls) != 0 {
		t.Fatalf("expected uninstalls cancelled, got %#v", entry.Uninstalls)
	}
}

func TestGetOptionalItems(t *testing.T) {
	origManifestGet := manifestGet
	defer func() { manifestGet = origManifestGet }()

	cfg := config.Configuration{
		AppDataPath: filepath.Clean(t.TempDir()),
	}

	manifestGet = func(_ config.Configuration) ([]manifest.Item, []string, error) {
		return []manifest.Item{
			{
				Name:             "base",
				Installs:         []string{"Firefox"},
				OptionalInstalls: []string{"GoogleChrome", "7zip", "Firefox"},
			},
			{
				Name:             "extra",
				OptionalInstalls: []string{"7zip", "VSCode"},
			},
		}, nil, nil
	}
	origCatalog := catalogGet
	defer func() { catalogGet = origCatalog }()
	catalogGet = func(_ config.Configuration) (map[int]map[string]catalog.Item, error) {
		return map[int]map[string]catalog.Item{}, nil
	}

	items, err := getOptionalItems(cfg)
	if err != nil {
		t.Fatalf("getOptionalItems failed: %v", err)
	}
	names := make([]string, 0, len(items))
	for _, it := range items {
		names = append(names, it.ItemName)
	}
	expected := []string{"7zip", "Firefox", "GoogleChrome", "VSCode"}
	if !reflect.DeepEqual(expected, names) {
		t.Fatalf("unexpected optional items, expected %#v, got %#v", expected, names)
	}
	// Only an item an admin manifest requires is marked required.
	for _, it := range items {
		if it.IsRequired != (it.ItemName == "Firefox") {
			t.Fatalf("%s isRequired = %v", it.ItemName, it.IsRequired)
		}
	}
}

func TestExecuteCommandRunPassesCfgThrough(t *testing.T) {
	cfg := config.Configuration{
		AppDataPath:    filepath.Clean(t.TempDir()),
		LocalManifests: []string{"already-local.yaml"},
	}

	var gotCfg config.Configuration
	managedRun := func(in config.Configuration, progress installer.ProgressFn, _ *installer.Cancels) (*report.Report, error) {
		gotCfg = in
		if progress != nil {
			t.Fatal("ordinary run unexpectedly received progress callback")
		}
		return nil, nil
	}

	resp, err := executeCommand(cfg, Command{Action: actionRun}, managedRun)
	if err != nil {
		t.Fatalf("executeCommand(run) failed: %v", err)
	}
	if resp.Status != "ok" {
		t.Fatalf("expected status ok, got %q", resp.Status)
	}
	if !reflect.DeepEqual(gotCfg.LocalManifests, cfg.LocalManifests) {
		t.Fatalf("expected managed run cfg local manifests %#v, got %#v", cfg.LocalManifests, gotCfg.LocalManifests)
	}
}

func TestExecuteCommandInstallWritesManifestAndDoesNotRunInline(t *testing.T) {
	cfg := config.Configuration{
		AppDataPath:    filepath.Clean(t.TempDir()),
		LocalManifests: []string{"already-local.yaml"},
	}
	stubOptional(t, "GoogleChrome")

	managedRunCalled := false
	managedRun := func(in config.Configuration, _ installer.ProgressFn, _ *installer.Cancels) (*report.Report, error) {
		managedRunCalled = true
		return nil, nil
	}

	resp, err := executeCommand(cfg, Command{Action: actionInstallItem, Items: []string{"GoogleChrome"}}, managedRun)
	if err != nil {
		t.Fatalf("executeCommand(install) failed: %v", err)
	}
	if resp.Status != "ok" {
		t.Fatalf("expected status ok, got %q", resp.Status)
	}

	if got := loadManifest(t, cfg).Installs; !reflect.DeepEqual(got, []string{"GoogleChrome"}) {
		t.Fatalf("unexpected service-manifest items: %#v", got)
	}

	if managedRunCalled {
		t.Fatalf("expected managed run to be deferred, but it ran inline")
	}
}

// A cancel puts back exactly what the request replaced, so a later scheduled
// run neither performs the request nor loses state the item had before it.
func TestWithdrawItemRevertsTheRequestExactly(t *testing.T) {
	cases := []struct {
		name       string
		before     manifest.Item
		request    func(config.Configuration) (selection, error)
		requested  selection
		wantBefore manifest.Item
	}{
		{
			name:   "fresh install",
			before: manifest.Item{Installs: []string{"7zip"}},
			request: func(cfg config.Configuration) (selection, error) {
				_, p, err := addServiceManagedInstall(cfg, "GoogleChrome")
				return p, err
			},
			requested: selectedForInstall,
		},
		{
			name:   "install of an item pending removal",
			before: manifest.Item{Uninstalls: []string{"GoogleChrome"}},
			request: func(cfg config.Configuration) (selection, error) {
				_, p, err := addServiceManagedInstall(cfg, "GoogleChrome")
				return p, err
			},
			requested: selectedForInstall,
		},
		{
			name:   "removal of a selected item",
			before: manifest.Item{Installs: []string{"7zip", "GoogleChrome"}},
			request: func(cfg config.Configuration) (selection, error) {
				return removeServiceManagedInstall(cfg, "GoogleChrome")
			},
			requested: selectedForRemoval,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Configuration{AppDataPath: t.TempDir()}
			stubOptional(t, "GoogleChrome", "7zip")
			if err := manifest.SaveSelfServe(serviceLocalManifestPath(cfg), tc.before); err != nil {
				t.Fatal(err)
			}
			prior, err := tc.request(cfg)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			if err := withdrawItem(cfg, installer.NewCancels(), "GoogleChrome", prior, tc.requested); err != nil {
				t.Fatalf("withdrawItem failed: %v", err)
			}
			got := loadManifest(t, cfg)
			if !slices.Equal(got.Installs, tc.before.Installs) || !slices.Equal(got.Uninstalls, tc.before.Uninstalls) {
				t.Fatalf("after cancel installs=%v uninstalls=%v, want %v/%v", got.Installs, got.Uninstalls, tc.before.Installs, tc.before.Uninstalls)
			}
		})
	}
}

// When a run got to the item first the cancel is refused and the request's
// selection stays, so the selection matches what the run is doing.
func TestWithdrawItemRefusedKeepsTheRequest(t *testing.T) {
	cfg := config.Configuration{AppDataPath: t.TempDir()}
	stubOptional(t, "GoogleChrome")
	_, prior, err := addServiceManagedInstall(cfg, "GoogleChrome")
	if err != nil {
		t.Fatal(err)
	}
	// A nil Cancels refuses every withdrawal, as one does for an acted-on item.
	if err := withdrawItem(cfg, nil, "GoogleChrome", prior, selectedForInstall); !errors.Is(err, errNotCancelable) {
		t.Fatalf("withdrawItem error = %v, want errNotCancelable", err)
	}
	if got := loadManifest(t, cfg).Installs; !slices.Equal(got, []string{"GoogleChrome"}) {
		t.Fatalf("a refused cancel changed the selection: %v", got)
	}
}
