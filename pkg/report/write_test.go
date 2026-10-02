package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestWriteInventoryReplacesTarget: each run must fully replace the previous
// inventory and leave no temp files behind in ProgramData.
func TestWriteInventoryReplacesTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "inventory.json")
	if err := os.WriteFile(path, []byte("planted"), 0o666); err != nil {
		t.Fatal(err)
	}

	if err := WriteInventory(path, New().Inventory(Plan{ManifestName: "site_default"})); err != nil {
		t.Fatalf("WriteInventory: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got Inventory
	if err = json.Unmarshal(data, &got); err != nil {
		t.Fatalf("target not replaced with inventory JSON: %v\n%s", err, data)
	}
	if got.ManifestName != "site_default" || got.SchemaVersion != SchemaVersion {
		t.Errorf("unexpected inventory: %#v", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("expected only inventory.json in %s, found %d entries", dir, len(entries))
	}
}
