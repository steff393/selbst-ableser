#!/bin/bash

set -e

DEVICE="$1"
MOUNTPOINT="/media/usb"
USER_NAME="__APP_USER__"

UID_NUM=$(id -u "$USER_NAME")
GID_NUM=$(id -g "$USER_NAME")

mkdir -p "$MOUNTPOINT"

if mountpoint -q "$MOUNTPOINT"; then
	exit 0
fi

FSTYPE=$(blkid -o value -s TYPE "$DEVICE" || true)

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
