//go:build windows

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The app data directory holds config.yaml (which may carry auth_user,
// auth_pass and TLS client keys), the self-serve manifest (the authorization
// state the pipe guards), the cache (installers that run as SYSTEM) and the
// log. ProgramData grants Users read and create rights, so the root gets a
// protected DACL: SYSTEM and Administrators only, inherited by everything
// below. bin additionally lets Users read and execute gorilla-ui.exe; they
// reach it through the bypass-traverse privilege. See docs/data-directory.md.
const (
	appDataSDDL = "O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"
	binSDDL     = "O:BAD:(A;OICI;FRFX;;;BU)"
	// inheritSDDL has no explicit ACEs: the entry keeps only what it inherits.
	inheritSDDL = "O:BAD:"
)

// protectAppData applies the data directory DACL to root and resets every
// entry below it to inherit from root, like `icacls /reset /t`. Owners become
// Administrators, so a file a user created before the fix loses the implicit
// WRITE_DAC its owner keeps. inventory.json keeps its stricter DACL. Symbolic
// links and junctions are skipped, never followed. It is idempotent and keeps
// going past entries it cannot fix, returning every error.
func protectAppData(root string) error {
	// CEILING: only a directory named gorilla is protected, so a mistyped
	// app_data_path such as C:\ProgramData cannot have its ACL replaced.
	// Upgrade: an explicit config opt-in for other names if anyone needs one.
	if !strings.EqualFold(filepath.Base(root), "gorilla") {
		return fmt.Errorf("not protecting %s: the directory is not named gorilla", root)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	// A junction here would point the ACL at somebody else's directory.
	if fi, err := os.Lstat(root); err != nil {
		return err
	} else if !fi.IsDir() {
		return fmt.Errorf("not protecting %s: not a plain directory", root)
	}
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		return err
	}

	// SYSTEM and elevated administrators hold the privilege but it is off by
	// default; without it, a file a user locked to themselves cannot be fixed.
	restore := enablePrivilege("SeTakeOwnershipPrivilege")
	defer restore()

	if err := setSecurity(root, appDataSDDL, windows.PROTECTED_DACL_SECURITY_INFORMATION); err != nil {
		return err
	}
	var errs []error
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			errs = append(errs, err)
			return nil
		}
		parent := filepath.Dir(path)
		switch {
		case path == root:
		case !d.IsDir() && !d.Type().IsRegular():
			errs = append(errs, fmt.Errorf("skipped %s: not a plain file or directory", path))
		case parent == root && strings.EqualFold(d.Name(), inventoryFile):
		case parent == root && d.IsDir() && strings.EqualFold(d.Name(), "bin"):
			errs = append(errs, setSecurity(path, binSDDL, windows.UNPROTECTED_DACL_SECURITY_INFORMATION))
		default:
			errs = append(errs, setSecurity(path, inheritSDDL, windows.UNPROTECTED_DACL_SECURITY_INFORMATION))
		}
		return nil
	})
	return errors.Join(append(errs, walkErr)...)
}

// setSecurity sets the owner, then the DACL, from sddl. The owner goes first:
// with the take-ownership privilege that works on any file, and the new owner
// (Administrators, which SYSTEM's token also holds) may then write the DACL.
func setSecurity(path, sddl string, protection windows.SECURITY_INFORMATION) error {
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, owner, nil, nil, nil); err != nil {
		return &os.PathError{Op: "set owner", Path: path, Err: err}
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|protection, nil, nil, dacl, nil); err != nil {
		return &os.PathError{Op: "set DACL", Path: path, Err: err}
	}
	return nil
}

// enablePrivilege enables name on the process token, best effort, and returns
// a function that restores the previous state.
func enablePrivilege(name string) func() {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &token); err != nil {
		return func() {}
	}
	tp := windows.Tokenprivileges{PrivilegeCount: 1}
	tp.Privileges[0].Attributes = windows.SE_PRIVILEGE_ENABLED
	if err := windows.LookupPrivilegeValue(nil, windows.StringToUTF16Ptr(name), &tp.Privileges[0].Luid); err != nil {
		_ = token.Close()
		return func() {}
	}
	var prev windows.Tokenprivileges
	var n uint32
	if err := windows.AdjustTokenPrivileges(token, false, &tp, uint32(unsafe.Sizeof(prev)), &prev, &n); err != nil {
		_ = token.Close()
		return func() {}
	}
	return func() {
		if prev.PrivilegeCount > 0 {
			_ = windows.AdjustTokenPrivileges(token, false, &prev, 0, nil, nil)
		}
		_ = token.Close()
	}
}
