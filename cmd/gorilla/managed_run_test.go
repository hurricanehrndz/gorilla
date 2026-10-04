package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/1dustindavis/gorilla/pkg/catalog"
	"github.com/1dustindavis/gorilla/pkg/config"
	"github.com/1dustindavis/gorilla/pkg/gorillalog"
	"github.com/1dustindavis/gorilla/pkg/manifest"
	"github.com/1dustindavis/gorilla/pkg/report"
)

// TestManagedRunReconcilesSelfServe verifies the run loads, reconciles, and
// saves the self-serve manifest — asserting a server default once (R4) even in
// check-only, and authorizing installs against the admin optional list (R2).
func TestManagedRunReconcilesSelfServe(t *testing.T) {
	resetMainHooks()
	defer resetMainHooks()
	// Deferred, not t.Cleanup: cleanups run after this function returns, so the
	// log handle would still be open when t.TempDir removes its directory. That
	// is fine on POSIX but fails on Windows, where an open file cannot be
	// unlinked. Defers run before cleanups, so the log closes first.
	defer gorillalog.Close()

	const manifestYAML = `name: wiretest_manifest
default_installs:
  - DemoDefault
optional_installs:
  - DemoDefault
  - DemoOptional
catalogs:
  - wiretest_catalog
`
	const catalogYAML = `DemoDefault:
  display_name: Demo Default
  check:
    file:
      - path: C:\does-not-exist\demodefault.txt
  installer:
    type: ps1
    location: packages/demodefault.ps1
    hash: deadbeef
DemoOptional:
  display_name: Demo Optional
  installer:
    type: ps1
    location: packages/demooptional.ps1
    hash: deadbeef
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/manifests/wiretest_manifest.yaml"):
			_, _ = w.Write([]byte(manifestYAML))
		case strings.HasSuffix(r.URL.Path, "/catalogs/wiretest_catalog.yaml"):
			_, _ = w.Write([]byte(catalogYAML))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	appData := t.TempDir()
	cfg := config.Configuration{
		CheckOnly:   true,
		CachePath:   t.TempDir(),
		AppDataPath: appData,
		URL:         srv.URL + "/",
		URLPackages: srv.URL + "/",
		Manifest:    "wiretest_manifest",
		Catalogs:    []string{"wiretest_catalog"},
	}

	adminCheckFunc = func() (bool, error) { return true, nil }
	mkdirAllFunc = func(string, os.FileMode) error { return nil }

	if _, err := managedRun(cfg, nil, nil); err != nil {
		t.Fatalf("managedRun failed: %v", err)
	}

	// The default must be asserted into the self-serve file even in check-only.
	entry, err := manifest.LoadSelfServe(manifest.SelfServePath(appData))
	if err != nil {
		t.Fatalf("load self-serve: %v", err)
	}
	if !contains(entry.Installs, "DemoDefault") {
		t.Errorf("expected DemoDefault in managed_installs, got %#v", entry.Installs)
	}
	if !contains(entry.DefaultInstalls, "DemoDefault") {
		t.Errorf("expected DemoDefault in default_installs record, got %#v", entry.DefaultInstalls)
	}

	// A real run attaches the supplied callback to Runner.Emit. The fixture's
	// deliberately invalid package hash still emits downloading/failed records,
	// which is enough to prove the run-scoped seam without changing sequencing.
	cfg.CheckOnly = false
	var states []string
	if _, err := managedRun(cfg, func(_ catalog.Item, state string, _ int, _ string) { states = append(states, state) }, nil); err != nil {
		t.Fatalf("managedRun with progress callback failed: %v", err)
	}
	if len(states) == 0 || states[0] != "downloading" {
		t.Fatalf("progress callback did not reach Runner.Emit: %v", states)
	}

	// The real run's inventory covers the default it tried (and failed, on the
	// bad hash) and the optional the user never selected.
	inv := readInventory(t, appData)
	if inv.ManifestName != "wiretest_manifest" {
		t.Errorf("ManifestName = %q", inv.ManifestName)
	}
	byName := make(map[string]report.InventoryItem)
	for _, it := range inv.ManagedInstalls {
		byName[it.Name] = it
	}
	if it := byName["DemoDefault"]; it.Kind != report.KindDefaultInstall || it.Status != report.StatusFailed || !it.SelfService || it.DisplayName != "Demo Default" {
		t.Errorf("DemoDefault = %#v", it)
	}
	if it := byName["DemoOptional"]; it.Kind != report.KindOptionalInstall || it.Status != report.StatusAvailable || it.SelfService {
		t.Errorf("DemoOptional = %#v", it)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
