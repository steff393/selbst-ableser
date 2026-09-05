#!/bin/bash
# Unmounts the USB stick mounted by selbst-ableser-usb-mount.sh. Invoked
# by udev via deploy/udev/99-selbst-ableser-usb.rules on device removal —
# not meant to be run by hand.
set -u

MOUNTPOINT="/media/usb"
LOGFILE="/var/log/selbst-ableser-usb-mount.log"

log() {
    echo "$(date '+%Y-%m-%d %H:%M:%S') [usb-umount] $*" >> "$LOGFILE"
}

log "started"

if mountpoint -q "$MOUNTPOINT"; then
    if umount "$MOUNTPOINT"; then
        log "done, $MOUNTPOINT unmounted"
    else
        log "ERROR: umount failed"
    fi
else
    log "nothing to do, $MOUNTPOINT not mounted"
fi
