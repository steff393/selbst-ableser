//go:build !linux && !windows

package removable

import "errors"

// ListMountPoints reports that automatic removable-media detection is not
// implemented on this platform. The USB-backup path simply never finds a
// stick here; the internal fallback backup.db location still works.
func ListMountPoints() ([]string, error) {
	return nil, errors.New("removable: automatic detection is not implemented on this platform")
}
