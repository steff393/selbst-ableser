#!/bin/bash
set -e

# Usage:
#   sudo git clone https://github.com/steff393/selbst-ableser /opt/selbst-ableser
#   sudo bash /opt/selbst-ableser/install.sh
#
# Uninstall:
#   sudo bash /opt/selbst-ableser/install.sh --uninstall
#
# Behavior (local / collector / evaluator) is controlled via cfg.ini, not via
# install flags. The same systemd unit runs in every mode.

APP_DIR="/opt/selbst-ableser"
VENV_DIR="$APP_DIR/venv"
APP_USER="selbst"

USB_DIR="$APP_DIR/usb"
USB_MOUNT="/media/usb"

GREEN='\033[0;32m'; YELLOW='\033[1;33m'; RED='\033[0;31m'; NC='\033[0m'
step() { echo -e "\n${GREEN}[$1] $2${NC}"; }
info() { echo -e "  ${YELLOW}→${NC} $1"; }
fail() { echo -e "\n${RED}FEHLER: $1${NC}"; exit 1; }

[ "$(id -u)" -eq 0 ] || fail "Bitte als root ausführen: sudo bash install.sh"


# ── Uninstall ────────────────────────────────────────────
if [ "${1}" == "--uninstall" ]; then
	info "Service stoppen, deaktivieren und löschen..."
	systemctl disable --now selbst-ableser 2>/dev/null || true
	systemctl disable --now selbst-ableser-email 2>/dev/null || true

	info "USB-Hotplug entfernen..."
	rm -f /etc/udev/rules.d/99-selbst-ableser-usb.rules
	rm -f /usr/local/bin/selbst-ableser-usb-mount.sh
	rm -f /usr/local/bin/selbst-ableser-usb-umount.sh
	systemctl unmask udisks2 2>/dev/null || true
	systemctl enable udisks2 2>/dev/null || true
	info "udisks2 wieder aktiviert"
	udevadm control --reload-rules 2>/dev/null || true

	umount "$USB_MOUNT" 2>/dev/null || true
	rmdir "$USB_MOUNT" 2>/dev/null || true

	info "Systemd Services entfernen..."
	rm -f /etc/systemd/system/selbst-ableser*.service
	systemctl daemon-reload

	info "User entfernen..."
	userdel "$APP_USER" 2>/dev/null || true

	info "Programmordner entfernen..."
	rm -rf "$APP_DIR"

	echo -e "\n${GREEN}Deinstallation abgeschlossen.${NC}"
	exit 0
fi


# ── Install ──────────────────────────────────────────────
echo ""
echo "╔══════════════════════════════════════╗"
echo "║    selbst-ableser Installation       ║"
echo "╚══════════════════════════════════════╝"

is_raspberry_pi() {
  grep -qi "raspberry pi" /proc/device-tree/model 2>/dev/null || \
  grep -qi "raspberry pi" /proc/cpuinfo       2>/dev/null
}


step 1 "Systempakete installieren"
apt-get update -q
apt-get install -y -q python3-venv udev util-linux
info "Pakete installiert"


step 2 "Journald auf RAM-only umstellen (schont SD-Karte)"
if is_raspberry_pi; then
  sed -i 's/^#*Storage=.*/Storage=volatile/'        /etc/systemd/journald.conf
  sed -i 's/^#*RuntimeMaxUse=.*/RuntimeMaxUse=20M/' /etc/systemd/journald.conf
	systemctl restart systemd-journald
  info "Pi erkannt – Logs nur noch im RAM (SD-Karte wird geschont)"
else
  info "Kein Pi – Journald-Konfiguration bleibt unverändert"
fi


step 3 "Unprivilegierten App-User anlegen"
if id "$APP_USER" &>/dev/null; then
	info "User '$APP_USER' existiert bereits – wird übersprungen"
else
	useradd --system --no-create-home --shell /usr/sbin/nologin "$APP_USER"
	info "User '$APP_USER' angelegt"
fi
chown -R "$APP_USER:$APP_USER" "$APP_DIR"
# dialout is needed for USB serial access in local/collector mode; harmless otherwise
usermod -a -G dialout "$APP_USER" 2>/dev/null || true
info "User '$APP_USER' zur Gruppe 'dialout' hinzugefügt"


step 4 "USB Hotplug System"
if is_raspberry_pi; then
	mkdir -p "$USB_MOUNT"

	install -m 755 "$USB_DIR/usb-mount.sh" \
		/usr/local/bin/selbst-ableser-usb-mount.sh

	install -m 755 "$USB_DIR/usb-umount.sh" \
		/usr/local/bin/selbst-ableser-usb-umount.sh
	install -m 644 "$USB_DIR/99-selbst-ableser-usb.rules" \
		/etc/udev/rules.d/99-selbst-ableser-usb.rules

	# Disable udisks2 to prevent conflicts with our custom mount/umount scripts
	systemctl disable --now udisks2 2>/dev/null || true
  systemctl mask udisks2 2>/dev/null || true
   info "udisks2 deaktiviert und maskiert"

	udevadm control --reload-rules
	info "USB Hotplug installiert"
else
	info "Kein Pi – USB Hotplug wird übersprungen"
fi

step 5 "Python-Umgebung einrichten"
python3 -m venv "$VENV_DIR"
chown -R "$APP_USER:$APP_USER" "$VENV_DIR"
info "pip upgraden..."
"$VENV_DIR/bin/pip" install --upgrade pip -q
info "requirements.txt installieren..."
"$VENV_DIR/bin/pip" install -r "$APP_DIR/requirements.txt" -q


step 6 "Systemd-Services einrichten"
cp "$APP_DIR/services/selbst-ableser.service"       /etc/systemd/system/
cp "$APP_DIR/services/selbst-ableser-email.service" /etc/systemd/system/
if is_raspberry_pi; then
	sed -i "s|--host 127.0.0.1|--host 0.0.0.0|" /etc/systemd/system/selbst-ableser.service
	sed -i "s|DEPLOYMENT_ENV=production|DEPLOYMENT_ENV=development|" /etc/systemd/system/selbst-ableser.service
fi

# USB Zugriff erlauben
grep -q "/media/usb" /etc/systemd/system/selbst-ableser.service || \
	sed -i 's|ReadWritePaths=|& /media/usb|' /etc/systemd/system/selbst-ableser.service

systemctl daemon-reload
systemctl restart selbst-ableser selbst-ableser-email # restart also starts, if already running
systemctl enable  selbst-ableser selbst-ableser-email

step 7 "Admin-Token anzeigen"
sleep 3
if [ -f "$APP_DIR/users.json" ]; then
	TOKEN=$(python3 -c "import json; d=json.load(open('$APP_DIR/users.json')); print(list(d.keys())[0])")
	info "Admin-Token: $TOKEN"
	info "Bitte sicher notieren bzw. speichern!"
else
	info "Noch keine users.json — Token nach erstem Start verfügbar (nur in Mode=local/evaluator)"
fi

echo ""
echo "╔══════════════════════════════════════╗"
echo "║      Installation abgeschlossen!     ║"
echo "╚══════════════════════════════════════╝"
echo ""
echo -e "  Modus ändern:   ${YELLOW}sudo nano $APP_DIR/cfg.ini${NC}  (Zeile 'Mode = ...')"
echo -e "  Status prüfen:  ${YELLOW}sudo systemctl status selbst-ableser${NC}"
echo -e "  Logs anzeigen:  ${YELLOW}sudo journalctl -u selbst-ableser -f${NC}"
echo -e "  Deinstallieren: ${YELLOW}sudo bash $APP_DIR/install.sh --uninstall${NC}"
echo ""
