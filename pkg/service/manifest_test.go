package service

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/1dustindavis/gorilla/pkg/config"
	"github.com/1dustindavis/gorilla/pkg/manifest"
)

// stubOptional overrides manifestGet so the service authorizes the given names
// as available optional installs, restoring the original on cleanup.
func stubOptional(t *testing.T, names ...string) {
	t.Helper()
	orig := manifestGet
	t.Cleanup(func() { manifestGet = orig })
	manifestGet = func(_ config.Configuration) ([]manifest.Item, []string, error) {
		return []manifest.Item{{Name: "base", OptionalInstalls: names}}, nil, nil
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

	if err := addServiceManagedInstalls(cfg, []string{"GoogleChrome", "7zip"}); err != nil {
		t.Fatalf("addServiceManagedInstalls failed: %v", err)
	}
	if err := addServiceManagedInstalls(cfg, []string{"GoogleChrome"}); err != nil {
		t.Fatalf("addServiceManagedInstalls dedupe failed: %v", err)
	}

	if got := loadManifest(t, cfg).Installs; !reflect.DeepEqual(got, []string{"7zip", "GoogleChrome"}) {
		t.Fatalf("unexpected installs after add: %#v", got)
	}

	if err := removeServiceManagedInstalls(cfg, []string{"GoogleChrome"}); err != nil {
		t.Fatalf("removeServiceManagedInstalls failed: %v", err)
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

	if err := addServiceManagedInstalls(cfg, []string{"NotOptional"}); err == nil {
		t.Fatalf("expected authorization error for unavailable item")
	}
	// Nothing should have been written.
	if got := loadManifest(t, cfg).Installs; len(got) != 0 {
		t.Fatalf("expected no installs written on rejection, got %#v", got)
	}
}

// TestAddCancelsPendingUninstall verifies re-selecting a removed item cancels
// its pending uninstall so the two lists stay disjoint (R3).
func TestAddCancelsPendingUninstall(t *testing.T) {
	cfg := config.Configuration{AppDataPath: filepath.Clean(t.TempDir())}
	stubOptional(t, "GoogleChrome")

	if err := addServiceManagedInstalls(cfg, []string{"GoogleChrome"}); err != nil {
		t.Fatalf("add failed: %v", err)
	}
	if err := removeServiceManagedInstalls(cfg, []string{"GoogleChrome"}); err != nil {
		t.Fatalf("remove failed: %v", err)
	}
	if got := loadManifest(t, cfg).Uninstalls; !reflect.DeepEqual(got, []string{"GoogleChrome"}) {
		t.Fatalf("expected pending uninstall, got %#v", got)
	}
	if err := addServiceManagedInstalls(cfg, []string{"GoogleChrome"}); err != nil {
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
				OptionalInstalls: []string{"GoogleChrome", "7zip", "Firefox"},
			},
			{
				Name:             "extra",
				OptionalInstalls: []string{"7zip", "VSCode"},
			},
		}, nil, nil
	}

	items, err := getOptionalItems(cfg)
	if err != nil {
		t.Fatalf("getOptionalItems failed: %v", err)
	}
	expected := []string{"7zip", "Firefox", "GoogleChrome", "VSCode"}
	if !reflect.DeepEqual(expected, items) {
		t.Fatalf("unexpected optional items, expected %#v, got %#v", expected, items)
	}
}

func TestExecuteCommandRunPassesCfgThrough(t *testing.T) {
	cfg := config.Configuration{
		AppDataPath:    filepath.Clean(t.TempDir()),
		LocalManifests: []string{"already-local.yaml"},
	}

	var gotCfg config.Configuration
	managedRun := func(in config.Configuration) error {
		gotCfg = in
		return nil
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
	managedRun := func(in config.Configuration) error {
		managedRunCalled = true
		return nil
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
