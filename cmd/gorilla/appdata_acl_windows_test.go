//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// TestProtectAppData: after protectAppData, a standard user must have no
// access to anything in the data directory but bin, whatever ACLs and owners
// entries had before (as on a tree an older version created, where Users could
// create files), while inventory.json keeps its stricter DACL and a junction a
// user planted is not followed.
func TestProtectAppData(t *testing.T) {
	root := filepath.Join(t.TempDir(), "gorilla")
	mkdir(t, root, filepath.Join(root, "bin"), filepath.Join(root, "cache", "packages"))
	write(t, filepath.Join(root, "bin", "gorilla-ui.exe"), filepath.Join(root, "config.yaml"),
		filepath.Join(root, "cache", "packages", "setup.msi"), filepath.Join(root, "service-manifest.yaml"),
		filepath.Join(root, inventoryFile))
	// A planted manifest that only Everyone may read, and inventory.json as
	// pkg/report writes it.
	setDACL(t, filepath.Join(root, "service-manifest.yaml"), "D:P(A;;FA;;;WD)")
	setDACL(t, filepath.Join(root, inventoryFile), "D:P(A;;FA;;;SY)(A;;FR;;;BA)")
	inventoryBefore := daclSDDL(t, filepath.Join(root, inventoryFile))

	canary := filepath.Join(t.TempDir(), "canary")
	mkdir(t, canary)
	setDACL(t, canary, "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;WD)")
	canaryBefore := sddl(t, canary)
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(root, "cache", "j"), canary).CombinedOutput(); err != nil {
		t.Fatalf("mklink: %v: %s", err, out)
	}

	// Twice: the service applies it at every start.
	for range 2 {
		err := protectAppData(root)
		if err == nil || !strings.Contains(err.Error(), "not a plain file or directory") {
			t.Fatalf("protectAppData: %v, want only the junction reported", err)
		}
	}

	const (
		admins    = "O:BA"
		inherited = "(A;ID;FA;;;SY)(A;ID;FA;;;BA)"
		dirInh    = "(A;OICIID;FA;;;SY)(A;OICIID;FA;;;BA)"
	)
	want := map[string]string{
		root:                       admins + "D:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)",
		filepath.Join(root, "bin"): admins + "D:AI(A;OICI;0x1200a9;;;BU)" + dirInh,
		filepath.Join(root, "bin", "gorilla-ui.exe"):          admins + "D:AI(A;ID;0x1200a9;;;BU)" + inherited,
		filepath.Join(root, "cache"):                          admins + "D:AI" + dirInh,
		filepath.Join(root, "cache", "packages", "setup.msi"): admins + "D:AI" + inherited,
		filepath.Join(root, "config.yaml"):                    admins + "D:AI" + inherited,
		filepath.Join(root, "service-manifest.yaml"):          admins + "D:AI" + inherited,
	}
	for path, w := range want {
		if got := sddl(t, path); got != w {
			t.Errorf("%s:\n got %s\nwant %s", path, got, w)
		}
	}
	if got := daclSDDL(t, filepath.Join(root, inventoryFile)); got != inventoryBefore {
		t.Errorf("inventory.json changed: got %s, want %s", got, inventoryBefore)
	}
	if got := sddl(t, canary); got != canaryBefore {
		t.Errorf("junction target changed:\n got %s\nwant %s", got, canaryBefore)
	}
}

func TestProtectAppDataRefusesOtherNames(t *testing.T) {
	dir := t.TempDir()
	before := sddl(t, dir)
	if err := protectAppData(dir); err == nil {
		t.Fatal("protectAppData accepted a directory not named gorilla")
	}
	if got := sddl(t, dir); got != before {
		t.Errorf("ACL changed:\n got %s\nwant %s", got, before)
	}
}

func mkdir(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func write(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func setDACL(t *testing.T, path, s string) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString(s)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
}

func sddl(t *testing.T, path string) string {
	return securityString(t, path, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
}

func daclSDDL(t *testing.T, path string) string {
	return securityString(t, path, windows.DACL_SECURITY_INFORMATION)
}

func securityString(t *testing.T, path string, info windows.SECURITY_INFORMATION) string {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, info)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo %s: %v", path, err)
	}
	return sd.String()
}
