package backup

import (
	"strings"
	"testing"
)

func TestBaseDeviceName(t *testing.T) {
	cases := map[string]string{
		"sda1":      "sda",
		"sdb":       "sdb",
		"mmcblk0p1": "mmcblk0",
		"mmcblk0":   "mmcblk0",
		"nvme0n1p1": "nvme0n1",
	}
	for in, want := range cases {
		if got := baseDeviceName(in); got != want {
			t.Errorf("baseDeviceName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUnescapeMountPoint(t *testing.T) {
	if got := unescapeMountPoint(`/media/user/USB\040DRIVE`); got != "/media/user/USB DRIVE" {
		t.Errorf("unescapeMountPoint = %q", got)
	}
	if got := unescapeMountPoint("/media/usb"); got != "/media/usb" {
		t.Errorf("unescapeMountPoint (no escape) = %q", got)
	}
}

func TestParseRemovableMounts(t *testing.T) {
	// A representative /proc/mounts excerpt: root on an SD card
	// (mmcblk0p2, not removable per its sysfs flag in this scenario —
	// SD cards report removable=0 on many Pi images since the kernel
	// treats the eMMC/SD controller as fixed), and a USB stick.
	mounts := strings.NewReader(strings.Join([]string{
		"/dev/mmcblk0p2 / ext4 rw,relatime 0 0",
		`/dev/sda1 /media/user/USB\040DRIVE vfat rw,relatime 0 0`,
		"tmpfs /run tmpfs rw,nosuid 0 0",
	}, "\n") + "\n")

	removable := func(device string) bool {
		return device == "sda"
	}

	got, err := parseRemovableMounts(mounts, removable)
	if err != nil {
		t.Fatalf("parseRemovableMounts: %v", err)
	}
	if len(got) != 1 || got[0] != "/media/user/USB DRIVE" {
		t.Errorf("got %v, want exactly the USB drive's mount point", got)
	}
}

func TestParseRemovableMountsNoneFound(t *testing.T) {
	mounts := strings.NewReader("/dev/mmcblk0p2 / ext4 rw,relatime 0 0\n")
	got, err := parseRemovableMounts(mounts, func(string) bool { return false })
	if err != nil {
		t.Fatalf("parseRemovableMounts: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want none", got)
	}
}

// TestParseRemovableMountsInsideSandbox reproduces what a service with
// ReadWritePaths=/media/usb actually reads: the bind mount of the empty
// directory, plus the stick's own mount propagated in twice. One stick,
// three lines — which used to count as several media and made AutoDetect
// refuse to pick any (the Collector page said "keiner" and the daily
// backup fell through to its internal file).
func TestParseRemovableMountsInsideSandbox(t *testing.T) {
	mounts := strings.NewReader(strings.Join([]string{
		"/dev/mmcblk0p2 / ext4 rw,noatime 0 0",
		"/dev/mmcblk0p2 /media/usb ext4 rw,nosuid,noatime 0 0",
		"/dev/sda1 /media/usb vfat rw,relatime,uid=999 0 0",
		"/dev/sda1 /media/usb vfat rw,relatime,uid=999 0 0",
	}, "\n") + "\n")

	got, err := parseRemovableMounts(mounts, func(device string) bool { return device == "sda" })
	if err != nil {
		t.Fatalf("parseRemovableMounts: %v", err)
	}
	if len(got) != 1 || got[0] != "/media/usb" {
		t.Errorf("got %v, want the stick's mount point exactly once", got)
	}
}

// TestParseRemovableMountsShadowedStick is the same mechanism the other
// way round: whatever is mounted last at a path is what writing there
// would reach, so a stick hidden underneath something else must not be
// reported as available.
func TestParseRemovableMountsShadowedStick(t *testing.T) {
	mounts := strings.NewReader(strings.Join([]string{
		"/dev/sda1 /media/usb vfat rw,relatime 0 0",
		"tmpfs /media/usb tmpfs rw,nosuid 0 0",
	}, "\n") + "\n")

	got, err := parseRemovableMounts(mounts, func(device string) bool { return device == "sda" })
	if err != nil {
		t.Fatalf("parseRemovableMounts: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want nothing: the stick is not what that path reaches", got)
	}
}
