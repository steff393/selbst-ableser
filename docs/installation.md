# Anleitung
selbst-ableser läuft als FastAPI-Anwendung. Was die Anwendung tut, wird über die Zeile `Mode = …` in `cfg.ini` entschieden:

| Mode | Was läuft | Typisches Szenario |
|---|---|---|
| `local` | Alles auf einem Gerät: USB-Empfang, Web-UI, Auswertung | Test unter Windows, Raspberry Pi komplett im Haus |
| `collector` | Nur USB-Empfang + Snapshot-Upload, **keine** AES-Schlüssel | Pi sammelt, lädt verschlüsselte Snapshots auf einen VPS |
| `evaluator` | Nur Web-UI + Auswertung, kein USB | VPS empfängt Snapshots vom Pi, entschlüsselt für Mieter |

In allen drei Fällen wird die gleiche Anwendung gestartet, am gleichen Port (Standard: 8282).

Es gibt zwei wesentliche Komponenten:
- Der **wM-Bus-Server** empfängt Telegramme und stellt sie auf einer Webseite (noch verschlüsselt dar). Außerdem speichert er täglich (oder monatlich) sogenannte Snapshots ab. Diese enthalten das zuletzt empfangene Telegramm jedes Zählers.  
- Der **Auswertungsserver** liest die Telegramme aus den Snapshots, entschlüsselt sie und bereit die Daten auf, z.B. für die Unterjährige Verbrauchsmitteilung (UVI). 
---

## Erste Schritte
Git-Repository klonen:  
`git clone https://github.com/steff393/selbst-ableser.git`

Virtuelle Umgebung anlegen, aktivieren und Abhängigkeiten installieren:  
`cd selbst-ableser`  
`python -m venv venv`  
`source venv/bin/activate` (Linux) bzw. `venv\Scripts\activate` (Windows)  
`pip install -r requirements.txt`  

Anwendung starten (gleicher Befehl in jedem Modus):  
`uvicorn app:app --port 8282`  
`uvicorn app:app --port 8282 --host 0.0.0.0`  zur Freigabe für alle Geräte im gleichen Netzwerk  

Beim ersten Start im Modus `local` oder `evaluator` legt die Anwendung `users.json` an und gibt einen Admin-Token aus:  
```
[selbst-ableser] Mode=local — Einzelplatz – alle Komponenten in einem Prozess
Userfile nicht gefunden – erstelle neue Datei...
========================================
Admin-Token erzeugt:
0123456789abcdef
Bitte sicher notieren bzw. speichern!
========================================
```

Im Modus `collector` wird kein Admin-Token erzeugt – dieser Prozess hat keine Web-UI für Mieter.  

Aufruf im Browser, z. B. `http://localhost:8282` (oder die IP-Adresse des Rechners).  
Das wM-Bus-Live-Dashboard ist unter `http://localhost:8282/wmbus/` erreichbar.  

---

## Modus `local` (Standard)
Alle Komponenten laufen auf einem Gerät. Dieser Modus ist der Standard nach dem Klonen und ist die einfachste Einstiegsvariante.

### Vorbereitung
Falls die automatische Erkennung des iU891A-XL-Empfängers nicht funktioniert, muss der entsprechende Port in `cfg.ini` eingetragen werden (oder `Port` auf einen leeren Wert gesetzt werden, falls testweise nur mit gespeicherten Snapshots gearbeitet wird). Der COM-Port lässt sich unter Linux wie folgt bestimmen:  
`ls -l /dev/serial/by-id/`  
Die Datei cfg.ini lässt sich unter Linux mit Nano öffnen:  
`nano cfg.ini`  
Die AES-Schlüssel der Zähler sind für diesen Schritt nicht erforderlich.  

### Ausgabe nach dem Start
```
[selbst-ableser] Mode=local — Einzelplatz – alle Komponenten in einem Prozess
INFO:     Uvicorn running on http://127.0.0.1:8282 (Press CTRL+C to quit)
Verbinde mit iU891A-XL an COM5...
-> C001030424C0 Get Device Info
<- C00104006E48090000BE2D0600C70B0000CF5DC0 Device Info OK
-> C00903030E0000003200A0BB0D00AF61C0 Set Configuration C1/T1
<- C0090400B23DC0 Configuration OK
Stick ist bereit und empfängt... (Strg+C zum Beenden)
12:53:38 ✔ Zähler 42130857 | RSSI -103 dBm | wmBus 32446850570813426980A011FF3249027004CE0170068E06204900496346373A1C1A1500000000000000000000001017143C40
Telegramm von 42130857 blockiert
12:53:42 ✔ Zähler 42130855 | RSSI -95 dBm | wmBus 32446850550813426980A011FF32B80270044F03A108820A177F007F80ADA95024303D02000000000000000000021033644974
```

### Benutzereinrichtung
Über den o.g. Link und mit Hilfe des Admin-Tokens kann nun die Login-Seite aufgerufen werden. Anschließend können verschiedene Nutzer gespeichert werden. Dafür ist eine Wohnungsnummer und die zugehörige Fläche anzugeben.  
Nutzer werden über Token (z.B. db7c01267a874140) unterschieden und zu Wohnungen zugeteilt.  
Admin-Nutzer sind Nutzer ohne hinterlegte Wohnung. 

### Konfiguration der Zählerplätze
Zählerplätze und Zähler können in einer Excel-Datei verwaltet werden. Zu jedem Zählerplatz können dort beliebig viele Zähler hinterlegt werden, so dass auch Zählerwechsel berücksichtigt werden können.  
Eine Beispiel-Datei findet sich unter Releases. 
Über den Daten-Import kann eine Excel-Datei importiert und mit einem Passwort verschlüsselt gespeichert werden. Das Passwort kann dem Auswertungsserver über unterschiedliche Methoden übergeben werden (siehe [locations.md](locations.md)).  

### Testmodus
Sobald zusätzlich zum Admin ein Nutzer mit Wohnung = 1 angelegt ist und in der Datei `cfg.ini` der Parameter `SnapshotDir` noch auf `tests/snapshots` zeigt, kann über die Benutzerverwaltung eine UVI mit den mitgelieferten Testdaten erzeugt und angezeigt werden:

```
# Verzeichnis für JSON-Snapshots
#SnapshotDir = snapshots
SnapshotDir = tests/snapshots
```

---

## Modus `collector` (Pi sammelt für einen entfernten VPS)
In `cfg.ini`:
```
Mode = collector
UploadServer = https://meine-domain.de
UploadToken = ABCD1234EF567890...
```

In diesem Modus werden **keine** AES-Schlüssel auf dem Pi geladen. `users.json` und `locations.json.enc` werden nicht angelegt bzw. nicht gelesen. Die Anwendung empfängt nur Telegramme vom USB-Stick, schreibt Snapshots und lädt sie **optional** an einen konfigurierten `UploadServer` hoch.

Das Live-Dashboard unter `/wmbus/` zeigt Telegramme verschlüsselt, weil keine Schlüssel verfügbar sind. Wird `/` aufgerufen, leitet die Anwendung automatisch auf `/wmbus/` um.

Den Upload-Token erzeugt man **einmalig** auf dem Auswertungs-Server (Modus `evaluator`) über das Admin-UI und trägt ihn dann auf dem Sammler in `cfg.ini` ein.

---

## Modus `evaluator` (VPS empfängt vom Sammler)
In `cfg.ini`:
```
Mode = evaluator
```

In diesem Modus wird kein USB-Empfänger gestartet, `Port` wird ignoriert. Die Anwendung empfängt Snapshots vom Sammler über die Route `POST /wmbus/snapshots/upload` (Header `X-Snapshot-Upload-Token`), entschlüsselt die enthaltenen Telegramme mit den AES-Schlüsseln aus `locations.json.enc` und stellt sie über die Web-UI bereit.

---

## Freigabe für andere Geräte im gleichen Netzwerk
Mit `uvicorn app:app --port 8282` ist der Server nur vom eigenen Gerät erreichbar.  
Mit `--host 0.0.0.0` wird er für alle Geräte im gleichen Netzwerk freigegeben:  
`uvicorn app:app --port 8282 --host 0.0.0.0`

> **Hinweis:** `--host 0.0.0.0` macht den Server für **jedes** Netzwerk-Interface erreichbar. Auf einem Gerät, das gleichzeitig an einem öffentlichen Netz hängt (z. B. VPN, öffentliches WLAN), ist der Server damit auch von dort erreichbar.

---

# Automatische Installation unter Linux
```
sudo git clone https://github.com/steff393/selbst-ableser /opt/selbst-ableser
sudo bash /opt/selbst-ableser/install.sh
```

Das Skript erstellt die venv, installiert die Python-Pakete aus der requirements.txt und richtet zwei systemd-Services ein:

* `selbst-ableser` – die Hauptanwendung (Port 8282, Modus nach `cfg.ini`)
* `selbst-ableser-email` – der optionale monatliche E-Mail-Versand

Welches Verhalten der Server zeigt, steht ausschließlich in `cfg.ini`. Möchte man später den Modus umstellen, reicht:

```
sudo nano /opt/selbst-ableser/cfg.ini   # Zeile "Mode = …" ändern
sudo systemctl restart selbst-ableser
```

Mit folgendem (optionalen) Skript kann zusätzlich ein USB-Stick für das Backup der Snapshots automatisch gemountet werden (als `/mnt/usbbackup`). Der Stick muss vorher angeschlossen sein:  
`sudo bash /opt/selbst-ableser/install_usb.sh`

### Deinstallation
`sudo bash /opt/selbst-ableser/install.sh --uninstall`

---

# Installation auf Plesk
Domain → Hosting & DNS → Apache & nginx Einstellungen
- "Proxy Mode" deaktivieren → Apply (vor nächstem Schritt)
- Zusätzliche nginx-Direktiven:
```
location / {
	proxy_pass http://127.0.0.1:8282;
	proxy_set_header Host $host;
	proxy_set_header X-Real-IP $remote_addr;
	proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
	proxy_set_header X-Forwarded-Proto $scheme;
}
```

Auf dem VPS sollte zusätzlich `DEPLOYMENT_ENV=production` in `/etc/systemd/system/selbst-ableser.service` gesetzt sein (Standard im ausgelieferten Unit ist `development`). Das aktiviert die TrustedHost-Middleware und schreibt Audit-Logs nach `audit.log`.

---

# Autostart und Logs unter Linux
Starten / stoppen:
```
systemctl start   selbst-ableser
systemctl stop    selbst-ableser
systemctl restart selbst-ableser
```

Autostart beim Booten:
```
systemctl enable  selbst-ableser
systemctl disable selbst-ableser         # aber läuft noch bis zum nächsten Reboot
systemctl enable  --now selbst-ableser   # gleichzeitig starten und aktivieren
systemctl disable --now selbst-ableser   # gleichzeitig stoppen und deaktivieren
```

Status und Logs prüfen
```
systemctl status selbst-ableser           # läuft die App?
journalctl -u selbst-ableser -f           # Live-Logs
journalctl -u selbst-ableser -n 20        # letzte 20 Zeilen
journalctl -u selbst-ableser -n 20 --no-pager  # ohne Pager
```
