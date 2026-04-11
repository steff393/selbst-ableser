#!/bin/bash
set -e

APP_USER="selbst"
MOUNT_POINT="/mnt/usbbackup"

if [ "$EUID" -ne 0 ]; then
    echo "Bitte mit sudo ausführen."
    exit 1
fi

# find usb device (only first with UUID and FAT/exFAT)
read -r DEV_NAME USB_UUID USB_FSTYPE < <(lsblk -o NAME,UUID,FSTYPE,RM -rn \
    | awk '$4=="1" && $3!="" && $2!="" {print $1, $2, $3; exit}')

if [ -z "$USB_UUID" ]; then
    echo "Kein USB-Stick gefunden. Abbruch."
    exit 1
fi

echo "Gefunden: /dev/$DEV_NAME  UUID=$USB_UUID  Typ=$USB_FSTYPE"

# Cancel if not FAT32 or exFAT
if [[ "$USB_FSTYPE" != "vfat" && "$USB_FSTYPE" != "exfat" ]]; then
    echo "Unerwartetes Dateisystem: $USB_FSTYPE. Bitte FAT32 oder exFAT verwenden."
    exit 1
fi

# fstab update
if grep -q "UUID=$USB_UUID" /etc/fstab; then
    echo "UUID bereits in fstab vorhanden. Nichts zu tun."
    exit 0
fi

if grep -q "$MOUNT_POINT" /etc/fstab; then
    echo "WARNUNG: $MOUNT_POINT bereits in fstab. Bitte manuell prüfen:"
    grep "$MOUNT_POINT" /etc/fstab
    exit 1
fi

mkdir -p "$MOUNT_POINT"
cp /etc/fstab /etc/fstab.bak
UID_USER=$(id -u "$APP_USER")
GID_USER=$(id -g "$APP_USER")
echo "UUID=$USB_UUID  $MOUNT_POINT  $USB_FSTYPE  uid=$UID_USER,gid=$GID_USER,nofail,noauto,x-systemd.automount  0  0" >> /etc/fstab
systemctl daemon-reload
systemctl start mnt-usbbackup.automount

echo "Fertig. fstab aktualisiert (Backup: /etc/fstab.bak)."
