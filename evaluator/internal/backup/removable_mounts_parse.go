package backup

import (
	"bufio"
	"io"
	"regexp"
	"strings"
)

// parseRemovableMounts is the platform-independent core of the Linux
// removable-media detection: given /proc/mounts content and a predicate
// that reports whether a given block device name (e.g. "sda1") is
// removable, it returns the mount points backed by a removable device.
// Kept free of any actual file I/O so it can be unit-tested on any
// platform; see removable_linux.go for the real /proc and /sys access.
//
// One mount point counts once, no matter how many lines mention it: a
// later mount shadows an earlier one at the same path, so only the last
// line describes what is actually reachable there. That is not a corner
// case but the normal picture inside a systemd sandbox — a unit with
// ReadWritePaths=/media/usb bind-mounts that directory at start, the
// stick's own mount then propagates in on top of it, and the same
// filesystem ends up listed three times. Counting those separately made
// one stick look like several media, and AutoDetect then refuses to pick
// any of them: the display said "no stick" and the daily backup quietly
// wrote to its internal fallback instead (DATEN-06).
func parseRemovableMounts(r io.Reader, removable func(device string) bool) ([]string, error) {
	var order []string
	device := make(map[string]string)

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		mountPoint := unescapeMountPoint(fields[1])
		// Non-block sources (tmpfs, overlay, ...) are tracked too, with an
		// empty device name: one of them mounted over a stick hides it
		// just as effectively as another block device would.
		name := ""
		if dev, ok := strings.CutPrefix(fields[0], "/dev/"); ok {
			name = baseDeviceName(dev)
		}
		if _, seen := device[mountPoint]; !seen {
			order = append(order, mountPoint)
		}
		device[mountPoint] = name
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	var out []string
	for _, mountPoint := range order {
		if name := device[mountPoint]; name != "" && removable(name) {
			out = append(out, mountPoint)
		}
	}
	return out, nil
}

var (
	partitionSuffix = regexp.MustCompile(`^([a-z]+)\d+$`)
	mmcblkPartition = regexp.MustCompile(`^(mmcblk\d+)p\d+$`)
	mmcblkWholeDisk = regexp.MustCompile(`^mmcblk\d+$`)
	nvmePartition   = regexp.MustCompile(`^(nvme\d+n\d+)p\d+$`)
	nvmeWholeDisk   = regexp.MustCompile(`^nvme\d+n\d+$`)
)

// baseDeviceName strips a trailing partition number to get the
// /sys/block/<dev> entry a partition belongs to: "sda1" -> "sda",
// "mmcblk0p1" -> "mmcblk0", "nvme0n1p1" -> "nvme0n1". A name that already
// looks like a whole-disk device — including "mmcblk0" and "nvme0n1"
// themselves, which end in a digit but are not partitions of anything —
// is returned unchanged; the whole-disk checks must run before the
// generic trailing-digit stripper below, or they would be misread as
// their own (nonexistent) partition.
func baseDeviceName(dev string) string {
	if mmcblkWholeDisk.MatchString(dev) || nvmeWholeDisk.MatchString(dev) {
		return dev
	}
	if m := mmcblkPartition.FindStringSubmatch(dev); m != nil {
		return m[1]
	}
	if m := nvmePartition.FindStringSubmatch(dev); m != nil {
		return m[1]
	}
	if m := partitionSuffix.FindStringSubmatch(dev); m != nil {
		return m[1]
	}
	return dev
}

// unescapeMountPoint reverses the octal escaping /proc/mounts uses for
// spaces and other awkward characters in a mount point (e.g. "\040" for a
// space).
func unescapeMountPoint(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if v, ok := parseOctalByte(s[i+1 : i+4]); ok {
				b.WriteByte(v)
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func parseOctalByte(s string) (byte, bool) {
	if len(s) != 3 {
		return 0, false
	}
	var v int
	for _, c := range s {
		if c < '0' || c > '7' {
			return 0, false
		}
		v = v*8 + int(c-'0')
	}
	if v > 255 {
		return 0, false
	}
	return byte(v), true
}
