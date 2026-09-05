//go:build linux

package backup

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ListRemovableMountPoints returns the mount points of currently mounted
// removable storage (DATEN-06's USB stick), found by reading /proc/mounts
// and checking each backing block device's /sys/block/<dev>/removable
// flag — the standard, udev-independent way to tell a USB stick from the
// SD card the system itself is running from.
func ListRemovableMountPoints() ([]string, error) {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return nil, fmt.Errorf("backup: reading /proc/mounts: %w", err)
	}
	defer f.Close()
	return parseRemovableMounts(f, isDeviceRemovable)
}

func isDeviceRemovable(device string) bool {
	data, err := os.ReadFile("/sys/block/" + device + "/removable")
	if err != nil {
		return false
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(data)))
	return err == nil && v == 1
}
