package manifest

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadSelfServeMissingFileReturnsNamedEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service-manifest.yaml")

	entry, err := LoadSelfServe(path)
	if err != nil {
		t.Fatalf("LoadSelfServe on missing file failed: %v", err)
	}
	if entry.Name != "service-manifest" {
		t.Fatalf("expected default name %q, got %q", "service-manifest", entry.Name)
	}
	if len(entry.Installs) != 0 {
		t.Fatalf("expected empty installs, got %#v", entry.Installs)
	}
}

func TestSaveSelfServePersistsAndDropsLists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service-manifest.yaml")

	saved := Item{
		Name:            "service-manifest",
		Installs:        []string{"GoogleChrome"},
		Uninstalls:      []string{"7zip"},
		DefaultInstalls: []string{"DemoDefault"},
		// These must be dropped on save.
		Includes: []string{"other-manifest"},
		Catalogs: []string{"prod"},
		Updates:  []string{"SomeUpdate"},
	}
	if err := SaveSelfServe(path, saved); err != nil {
		t.Fatalf("SaveSelfServe failed: %v", err)
	}

	got, err := LoadSelfServe(path)
	if err != nil {
		t.Fatalf("LoadSelfServe failed: %v", err)
	}

	if !reflect.DeepEqual(got.Installs, []string{"GoogleChrome"}) {
		t.Fatalf("unexpected installs: %#v", got.Installs)
	}
	if !reflect.DeepEqual(got.Uninstalls, []string{"7zip"}) {
		t.Fatalf("expected Uninstalls persisted, got %#v", got.Uninstalls)
	}
	if !reflect.DeepEqual(got.DefaultInstalls, []string{"DemoDefault"}) {
		t.Fatalf("expected DefaultInstalls persisted, got %#v", got.DefaultInstalls)
	}
	// Save nils these lists; they marshal as `[]` and load back empty (not nil).
	if len(got.Includes) != 0 || len(got.Catalogs) != 0 || len(got.Updates) != 0 {
		t.Fatalf("expected includes/catalogs/updates dropped, got %#v", got)
	}
}
