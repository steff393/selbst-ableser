// Package removable detects a currently connected removable storage
// medium (a USB stick), on Windows and Linux. It is a deliberate copy of
// the evaluator module's internal/backup removable-media detection, not
// an import — see internal/telegram/doc.go for why the collector module
// duplicates rather than depends on the evaluator module for its small,
// non-sensitive, platform-specific pieces.
package removable

// AutoDetect picks a connected removable medium's mount point. Nothing
// connected is reported as such, without an error — a stick that is not
// plugged in right now may well be later. With more than one connected,
// it takes the first: /proc/mounts order on Linux (so, mount order),
// drive-letter order on Windows.
//
// Taking the first is deliberate, and deliberately unlike the receiver
// detection next door, which refuses to guess between two receivers and
// stops. The difference is who is present. A second receiver means the
// wrong *data*, and someone is starting the process and can fix it. The
// backup runs at 3 a.m. with nobody to ask, and refusing there does not
// buy clarity — it means no backup on any stick at all, silently, which
// is worse than a backup on whichever of two sticks was mounted first.
// Two removable media on a collector machine are rare; "then no backup"
// is not an acceptable answer to a rare case.
func AutoDetect() (string, bool, error) {
	mounts, err := ListMountPoints()
	if err != nil {
		return "", false, err
	}
	if len(mounts) == 0 {
		return "", false, nil
	}
	return mounts[0], true, nil
}
