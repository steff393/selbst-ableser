//go:build !linux && !windows

package backup

import "errors"

// ListRemovableMountPoints reports that automatic removable-media
// detection is not implemented on this platform (BETRIEB-01 only commits
// to Windows and Linux). Backing up still works everywhere via an
// explicitly named destination directory; only the auto-detection
// convenience is unavailable here.
func ListRemovableMountPoints() ([]string, error) {
	return nil, errors.New("backup: automatic removable-media detection is not implemented on this platform; name a destination directory explicitly")
}
