//go:build windows

package report

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// fileAllAccess is FILE_ALL_ACCESS, which SDDL "FA" maps to.
const fileAllAccess = 0x1F01FF

// TestWriteInventoryProtectedDACL: the inventory must be readable only by
// SYSTEM and Administrators, and a file a user planted at the target must not
// keep its ACL after the run.
func TestWriteInventoryProtectedDACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory.json")
	// The planted file inherits the temp directory's ACL, which grants the
	// current user access: exactly what must not survive.
	if err := os.WriteFile(path, []byte("planted"), 0o666); err != nil {
		t.Fatal(err)
	}

	if err := WriteInventory(path, New().Inventory(Plan{})); err != nil {
		t.Fatalf("WriteInventory: %v", err)
	}

	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo: %v", err)
	}
	t.Logf("SDDL: %s", sd.String())
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Errorf("DACL is not protected (inheritance enabled)")
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}

	want := map[windows.WELL_KNOWN_SID_TYPE]windows.ACCESS_MASK{
		windows.WinLocalSystemSid:           fileAllAccess,
		windows.WinBuiltinAdministratorsSid: windows.FILE_GENERIC_READ,
	}
	if dacl.AceCount != uint16(len(want)) {
		t.Errorf("DACL has %d ACEs, want %d", dacl.AceCount, len(want))
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatalf("GetAce %d: %v", i, err)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		account, domain, _, _ := sid.LookupAccount("")
		t.Logf("ACE %d: type=%d sid=%s (%s\\%s) mask=0x%X", i, ace.Header.AceType, sid, domain, account, ace.Mask)
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			t.Errorf("ACE %d is not an allow ACE", i)
			continue
		}
		matched := false
		for sidType, mask := range want {
			if sid.IsWellKnown(sidType) {
				matched = true
				if ace.Mask != mask {
					t.Errorf("ACE %d (%s): mask 0x%X, want 0x%X", i, sid, ace.Mask, mask)
				}
				delete(want, sidType)
			}
		}
		if !matched {
			t.Errorf("unexpected ACE %d for %s", i, sid)
		}
	}
	for sidType := range want {
		t.Errorf("missing ACE for well-known SID type %d", sidType)
	}
}
