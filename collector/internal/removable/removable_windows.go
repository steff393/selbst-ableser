//go:build windows

package removable

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	procGetLogicalDrives = kernel32.NewProc("GetLogicalDrives")
	procGetDriveTypeW    = kernel32.NewProc("GetDriveTypeW")
)

// driveRemovable is DRIVE_REMOVABLE from the Windows API (winbase.h).
const driveRemovable = 2

// ListMountPoints returns the drive roots (e.g. "E:\") of currently
// connected removable drives, via GetLogicalDrives/GetDriveTypeW.
func ListMountPoints() ([]string, error) {
	mask, _, callErr := procGetLogicalDrives.Call()
	if mask == 0 {
		return nil, fmt.Errorf("removable: GetLogicalDrives: %w", callErr)
	}

	var out []string
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		rootPtr, err := syscall.UTF16PtrFromString(root)
		if err != nil {
			continue
		}
		driveType, _, _ := procGetDriveTypeW.Call(uintptr(unsafe.Pointer(rootPtr)))
		if driveType == driveRemovable {
			out = append(out, root)
		}
	}
	return out, nil
}
