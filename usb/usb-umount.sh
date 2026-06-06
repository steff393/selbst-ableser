#!/bin/bash

MOUNTPOINT="/media/usb"

if mountpoint -q "$MOUNTPOINT"; then
	umount "$MOUNTPOINT" || true
fi
