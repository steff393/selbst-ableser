#!/bin/bash
set -e

# Usage:
# sudo git clone https://github.com/steff393/selbst-ableser /opt/selbst-ableser
# sudo bash /opt/selbst-ableser/install.sh --pi
#
# Uninstall:
# sudo bash /opt/selbst-ableser/install.sh --pi --uninstall

if [ "${1}" == "--pi" ]; then
  MODE="pi"
else
  MODE="vps"
fi
APP_DIR="/opt/selbst-ableser"
VENV_DIR="$APP_DIR/venv"
APP_USER="selbst"

GREEN='\033[0;32m'; YELLOW='\033[1;33m'; RED='\033[0;31m'; NC='\033[0m'
TOTAL=6
step() { echo -e "\n${GREEN}[$1/$TOTAL] $2${NC}"; }
info()  { echo -e "  ${YELLOW}→${NC} $1"; }
fail()  { echo -e "\n${RED}FEHLER: $1${NC}"; exit 1; }

[ "$(id -u)" -eq 0 ] || fail "Bitte als root ausführen: sudo bash install_$MODE.sh"


# ── Uninstall ─────────────────────────────────────────────
if [ "${2}" == "--uninstall" ]; then
	echo ""
	echo "╔══════════════════════════════════════╗"
	echo "║    selbst-ableser  Deinstallation    ║"
	echo "╚══════════════════════════════════════╝"

	info "Services stoppen und deaktivieren..."
	systemctl disable --now selbst-ableser-email 2>/dev/null || true
	systemctl disable --now selbst-ableser-$MODE 2>/dev/null || true
	if [ "$MODE" == "pi" ]; then
		systemctl disable --now selbst-ableser-wmbus 2>/dev/null || true
	fi

	info "Service-Dateien entfernen..."
	rm -f /etc/systemd/system/selbst-ableser*.service
	systemctl daemon-reload

	info "User entfernen..."
	userdel $APP_USER 2>/dev/null || true

	info "Programmordner entfernen..."
	rm -rf $APP_DIR

	echo -e "\n${GREEN}Deinstallation abgeschlossen.${NC}"
	exit 0
fi


# ── Install ───────────────────────────────────────────────
echo ""
echo "╔══════════════════════════════════════╗"
echo "║    selbst-ableser  Installation      ║"
echo "║                 $MODE                  ║"
echo "╚══════════════════════════════════════╝"


# ── 1. Pakete ──────────────────────────────────────────────
step 1 "Systempakete installieren"
apt-get update -q
apt-get install -y -q python3-venv
info "Pakete installiert"

# ── 2. Journald → RAM ─────────────────────────────────────
step 2 "Journald auf RAM-only umstellen (schont SD-Karte)"
if [ "$MODE" == "pi" ]; then
	sed -i 's/^#*Storage=.*/Storage=volatile/'        /etc/systemd/journald.conf
	sed -i 's/^#*RuntimeMaxUse=.*/RuntimeMaxUse=20M/' /etc/systemd/journald.conf
	systemctl restart systemd-journald
	info "Logs werden nur noch im RAM gespeichert"
else
	info "Nicht nötig auf VPS"
fi

# ── 3. App-User anlegen ───────────────────────────────────
step 3 "Unprivilegierten App-User anlegen"
if id "$APP_USER" &>/dev/null; then
	info "User '$APP_USER' existiert bereits – wird übersprungen"
else
	useradd --system --no-create-home --shell /usr/sbin/nologin $APP_USER
	info "User '$APP_USER' angelegt"
fi
chown -R $APP_USER:$APP_USER $APP_DIR
if [ "$MODE" == "pi" ]; then
  usermod -a -G dialout $APP_USER
  info "User '$APP_USER' zur Gruppe 'dialout' hinzugefügt"
fi
info "Dateiberechtigungen gesetzt"


# ── 4. venv + Abhängigkeiten ──────────────────────────────
step 4 "Python-Umgebung einrichten"
info "Venv erstellen..."
python3 -m venv "$VENV_DIR"
info "pip upgraden..."
"$VENV_DIR/bin/pip" install --upgrade pip -q
info "requirements.txt installieren..."
"$VENV_DIR/bin/pip" install -r "$APP_DIR/requirements.txt" -q
info "Python-Umgebung bereit"


# ── 5. Systemd-Services ───────────────────────────────────
step 5 "Systemd-Services einrichten"

cp $APP_DIR/services/selbst-ableser-email.service /etc/systemd/system/
cp $APP_DIR/services/selbst-ableser-$MODE.service /etc/systemd/system/
if [ "$MODE" == "pi" ]; then
	cp $APP_DIR/services/selbst-ableser-wmbus.service /etc/systemd/system/
fi

systemctl daemon-reload
systemctl restart selbst-ableser-email  selbst-ableser-$MODE # restart also starts, if already running
systemctl enable  selbst-ableser-email  selbst-ableser-$MODE
if [ "$MODE" == "pi" ]; then
 systemctl restart selbst-ableser-wmbus # restart also starts, if already running
 systemctl enable  selbst-ableser-wmbus
fi
info "Services gestartet und aktiviert"


# ── 6. Token Display ──────────────────────────────────────
step 6 "Admin-Token anzeigen"
sleep 10
echo ""
if [ -f "$APP_DIR/users.json" ]; then
TOKEN=$(python3 -c "import json; d=json.load(open('$APP_DIR/users.json')); print(list(d.keys())[0])")
info "Admin-Token: $TOKEN"
info "Bitte sicher notieren bzw. speichern!"
else
	info "Noch keine users.json gefunden – Token nach erstem Start verfügbar"
fi


# ── Fertig ────────────────────────────────────────────────
echo ""
echo "╔══════════════════════════════════════╗"
echo "║      Installation abgeschlossen!     ║"
echo "╚══════════════════════════════════════╝"
echo ""
echo -e "  Status prüfen:  ${YELLOW}systemctl status selbst-ableser-...${NC}"
echo -e "  Logs anzeigen:  ${YELLOW}journalctl -u status selbst-ableser-... -f${NC}"
echo -e "  Deinstallieren: ${YELLOW}sudo bash install.sh --$MODE --uninstall${NC}"
echo ""