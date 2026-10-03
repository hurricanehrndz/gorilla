package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// WriteInventory writes inv as indented JSON to path (see writeProtected).
func WriteInventory(path string, inv Inventory) error {
	data, err := json.MarshalIndent(inv, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal inventory: %w", err)
	}
	return writeProtected(path, data)
}

// writeProtected replaces path with data so the result carries only the
// inventory DACL. The temp file has that DACL from creation, before any data
// reaches it, and the rename replaces whatever sat at path, including a file
// planted there by a user, so the planter keeps neither the file nor its ACL.
func writeProtected(path string, data []byte) (err error) {
	tmp, err := createProtectedTemp(filepath.Dir(path), "."+filepath.Base(path)+".")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	// The handle was opened read/write at creation, so it can write even
	// though the DACL denies the caller (an admin, not SYSTEM) write access.
	if _, err = tmp.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err = os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}
