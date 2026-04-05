#!/bin/bash
set -e

# Usage:
# sudo git clone https://github.com/steff393/selbst-ableser /opt/selbst-ableser
# sudo bash /opt/selbst-ableser/install.sh

APP_DIR="/opt/selbst-ableser"
VENV_DIR="$APP_DIR/venv"

GREEN='\033[0;32m'; YELLOW='\033[1;33m'; RED='\033[0;31m'; NC='\033[0m'
TOTAL=5
step() { echo -e "\n${GREEN}[$1/$TOTAL] $2${NC}"; }
info()  { echo -e "  ${YELLOW}→${NC} $1"; }
fail()  { echo -e "\n${RED}FEHLER: $1${NC}"; exit 1; }

[ "$(id -u)" -eq 0 ] || fail "Bitte als root ausführen: sudo bash install.sh"

echo ""
echo "╔══════════════════════════════════════╗"
echo "║    selbst-ableser  Installation      ║"
echo "╚══════════════════════════════════════╝"

# ── 1. Pakete ──────────────────────────────────────────────
step 1 "Systempakete installieren"
apt-get update -q
apt-get install -y -q git python3-venv
info "Pakete installiert"

# ── 2. Journald → RAM ─────────────────────────────────────
step 2 "Journald auf RAM-only umstellen (schont SD-Karte)"
sed -i 's/^#*Storage=.*/Storage=volatile/'        /etc/systemd/journald.conf
sed -i 's/^#*RuntimeMaxUse=.*/RuntimeMaxUse=20M/' /etc/systemd/journald.conf
systemctl restart systemd-journald
info "Logs werden nur noch im RAM gespeichert"

# ── 3. Repository + venv ──────────────────────────────────
step 3 "App installieren"
info "Venv installieren..."
python3 -m venv "$VENV_DIR"
info "pip upgraden..."
"$VENV_DIR/bin/pip" install --upgrade pip -q
info "requirements.txt installieren..."
"$VENV_DIR/bin/pip" install -r "$APP_DIR/requirements.txt" -q
info "App bereit unter $APP_DIR"

# ── 4. FastAPI-Services ───────────────────────────────────
step 4 "Systemd-Services erstellen"

cp $APP_DIR/services/selbst-ableser-email.service /etc/systemd/system/
cp $APP_DIR/services/selbst-ableser-main.service  /etc/systemd/system/
cp $APP_DIR/services/selbst-ableser-wmbus.service /etc/systemd/system/

systemctl daemon-reload
systemctl restart selbst-ableser-email  selbst-ableser-main  selbst-ableser-wmbus # restart also starts, if already running
systemctl enable  selbst-ableser-email  selbst-ableser-main  selbst-ableser-wmbus
info "Services aktiviert"

# ── 5. Token Display ──────────────────────────────────────
step 5 "Admin-Token erzeugen"
sleep 10
echo ""
TOKEN=$(python3 -c "import json; d=json.load(open('$APP_DIR/users.json')); print(list(d.keys())[0])")
info "Admin-Token: $TOKEN"
info "Bitte sicher notieren bzw. speichern!"

# ── Fertig ────────────────────────────────────────────────
echo ""
echo "╔══════════════════════════════════════╗"
echo "║      Installation abgeschlossen!     ║"
echo "╚══════════════════════════════════════╝"
echo ""
