//go:build windows

package report

import (
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// inventorySDDL grants SYSTEM full control and Administrators read, with
// inheritance disabled (protected DACL). Nothing else.
const inventorySDDL = "D:P(A;;FA;;;SY)(A;;FR;;;BA)"

// createProtectedTemp creates a new file in dir, named prefix plus a random
// suffix, that carries inventorySDDL from the moment it exists. Setting the
// DACL after creation would leave a window in which the file has the
// directory's inherited ACL (Users can read under ProgramData) and a reader
// could open a handle that outlives the change. The file is opened without
// sharing, so nobody else can open it while it is being written.
// CEILING: single-purpose and unexported because the inventory is the only
// file Gorilla protects. Upgrade: when a second file or the ProgramData\gorilla
// directory needs explicit ACLs, move this to a shared package and take the
// SDDL as an argument.
func createProtectedTemp(dir, prefix string) (*os.File, error) {
	sd, err := windows.SecurityDescriptorFromString(inventorySDDL)
	if err != nil {
		return nil, err
	}
	sa := windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: sd,
	}
	for range 10 {
		name := filepath.Join(dir, prefix+rand.Text()+".tmp")
		path, err := windows.UTF16PtrFromString(name)
		if err != nil {
			return nil, err
		}
		h, err := windows.CreateFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, &sa,
			windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if errors.Is(err, windows.ERROR_FILE_EXISTS) {
			continue
		}
		if err != nil {
			return nil, &os.PathError{Op: "create", Path: name, Err: err}
		}
		return os.NewFile(uintptr(h), name), nil
	}
	return nil, errors.New("create protected temp file: too many name collisions")
}
