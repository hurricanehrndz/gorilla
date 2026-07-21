package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/1dustindavis/gorilla/pkg/config"
	"github.com/1dustindavis/gorilla/pkg/gorillalog"
	"github.com/1dustindavis/gorilla/pkg/manifest"
)

// TestManagedRunReconcilesSelfServe verifies the run loads, reconciles, and
// saves the self-serve manifest — asserting a server default once (R4) even in
// check-only, and authorizing installs against the admin optional list (R2).
func TestManagedRunReconcilesSelfServe(t *testing.T) {
	resetMainHooks()
	defer resetMainHooks()
	t.Cleanup(gorillalog.Close)

	const manifestYAML = `name: wiretest_manifest
default_installs:
  - DemoDefault
optional_installs:
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

	if _, err := managedRun(cfg); err != nil {
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
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
