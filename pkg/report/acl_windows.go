//go:build windows

package report

import "golang.org/x/sys/windows"

// inventorySDDL grants SYSTEM full control and Administrators read, with
// inheritance disabled (protected DACL). Nothing else.
const inventorySDDL = "D:P(A;;FA;;;SY)(A;;FR;;;BA)"

// protectFile replaces path's DACL with inventorySDDL.
// CEILING: single-purpose and unexported because the inventory is the only
// file Gorilla protects. Upgrade: when a second file or the ProgramData\gorilla
// directory needs explicit ACLs, move this to a shared package and take the
// SDDL as an argument.
func protectFile(path string) error {
	sd, err := windows.SecurityDescriptorFromString(inventorySDDL)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil)
}
