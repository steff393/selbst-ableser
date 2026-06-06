#!/bin/bash
MOUNTPOINT="/media/usb"
LOGFILE="/var/log/selbst-ableser-usb-mount.log"

log() {
    echo "$(date '+%Y-%m-%d %H:%M:%S') [usb-umount] $*" >> "$LOGFILE"
}

log "usb-umount.sh gestartet"

if mountpoint -q "$MOUNTPOINT"; then
    umount "$MOUNTPOINT"
    if [ $? -eq 0 ]; then
        log "Fertig – $MOUNTPOINT erfolgreich ausgehaengt"
    else
        log "FEHLER – umount fehlgeschlagen"
    fi
else
    log "Nichts zu tun – $MOUNTPOINT ist nicht gemountet"
fi
