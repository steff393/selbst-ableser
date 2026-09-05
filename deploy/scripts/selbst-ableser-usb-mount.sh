#!/bin/bash
# Mounts a hotplugged USB stick at a fixed path so saCollector's dynamic
# removable-media detection (which reads /proc/mounts, not a fixed path
# itself) always finds it in the same place the collector's systemd unit
# grants write access to. Invoked by udev via
# deploy/udev/99-selbst-ableser-usb.rules — not meant to be run by hand.
set -u

DEVICE="$1"
MOUNTPOINT="/media/usb"
USER_NAME="selbst"
LOGFILE="/var/log/selbst-ableser-usb-mount.log"

log() {
    echo "$(date '+%Y-%m-%d %H:%M:%S') [usb-mount] $*" >> "$LOGFILE"
}

log "started for $DEVICE"

# udev fires ACTION=="add" as soon as the partition device node exists,
# which can be slightly ahead of the kernel having finished reading its
# filesystem type — without this, blkid below sometimes sees an empty
# TYPE on a device that mounts fine a moment later.
sleep 2

UID_NUM=$(id -u "$USER_NAME")
GID_NUM=$(id -g "$USER_NAME")
mkdir -p "$MOUNTPOINT"

if mountpoint -q "$MOUNTPOINT"; then
    log "already mounted, nothing to do"
    exit 0
fi

FSTYPE=$(blkid -o value -s TYPE "$DEVICE" || true)
log "detected filesystem: $FSTYPE"

# Deliberately narrow: these are the ownerless-by-default filesystems a
# store-bought stick actually ships formatted as, which is why the mount
# options below map ownership to $USER_NAME explicitly. A stick formatted
# ext4 or similar falls through with no mount attempt and no error — the
# same "no stick found" state saCollector already handles by falling back
# to its internal backup.db.
case "$FSTYPE" in
    vfat|msdos)
        mount -t vfat \
            -o rw,uid="$UID_NUM",gid="$GID_NUM",umask=0000 \
            "$DEVICE" "$MOUNTPOINT"
        ;;
    exfat)
        mount -t exfat \
            -o rw,uid="$UID_NUM",gid="$GID_NUM",umask=0000 \
            "$DEVICE" "$MOUNTPOINT"
        ;;
    ntfs|ntfs3)
        mount -t ntfs3 \
            -o rw,uid="$UID_NUM",gid="$GID_NUM",umask=0000 \
            "$DEVICE" "$MOUNTPOINT"
        ;;
esac

log "done, $DEVICE mounted at $MOUNTPOINT"
