#!/bin/bash
DEVICE="$1"
MOUNTPOINT="/media/usb"
USER_NAME="selbst"
LOGFILE="/var/log/selbst-ableser-usb-mount.log"

log() {
    echo "$(date '+%Y-%m-%d %H:%M:%S') [usb-mount] $*" >> "$LOGFILE"
}

log "usb-mount.sh gestartet mit Parameter: $DEVICE"
sleep 2
log "Warten abgeschlossen"

UID_NUM=$(id -u "$USER_NAME")
GID_NUM=$(id -g "$USER_NAME")
mkdir -p "$MOUNTPOINT"

if mountpoint -q "$MOUNTPOINT"; then
  log "Bereits gemountet – Abbruch"
  exit 0
fi

FSTYPE=$(blkid -o value -s TYPE "$DEVICE" || true)
log "Erkanntes Dateisystem: $FSTYPE"

case "$FSTYPE" in
  vfat|msdos)
    mount -t vfat \
      -o rw,uid=$UID_NUM,gid=$GID_NUM,umask=0000 \
      "$DEVICE" "$MOUNTPOINT"
    ;;
  exfat)
    mount -t exfat \
      -o rw,uid=$UID_NUM,gid=$GID_NUM,umask=0000 \
      "$DEVICE" "$MOUNTPOINT"
    ;;
  ntfs|ntfs3)
    mount -t ntfs3 \
      -o rw,uid=$UID_NUM,gid=$GID_NUM,umask=0000 \
      "$DEVICE" "$MOUNTPOINT"
    ;;
esac

log "Fertig – $DEVICE gemountet auf $MOUNTPOINT"