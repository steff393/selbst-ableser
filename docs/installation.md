# Anleitung
Git-Repository klonen:  
`git clone https://github.com/steff393/selbst-ableser.git`  

Virtual Environment venv anlegen, aktivieren und Abhängigkeiten installieren:  
`cd selbst-ableser`  
`python -m venv venv`  
`source venv/bin/activate` (Linux) bzw. `venv\Scripts\activate` (Windows)  
`pip install -r requirements.txt`  

Nun können entweder der wM-Bus-Server oder der Auswertungs-Server gestartet werden.  

# wM-Bus-Server
Der wM-Bus-Server empfängt Telegramme und stellt sie auf einer Webseite (noch verschlüsselt dar). Außerdem speichert er täglich (oder monatlich) sogenannte Snapshots ab. Diese enthalten das zuletzt empfangene Telegramm jedes Zählers.  

#### Vorbereitung
In die Datei cfg.ini muss als Port der COM-Port des iU891A-XL-Empfängers eingetragen werden (oder Port auskommentiert werden, falls testweise nur mit Snapshots gearbeitet wird). Der COM-Port lässt sich unter Linux wie folgt bestimmen:  
`ls -l /dev/serial/by-id/`  
Die Datei cfg.ini lässt sich unter Linux mit Nano öffnen:  
`nano cfg.ini`  
Die AES-Schlüssel der Zähler sind für diesen Schritt nicht erforderlich.  

#### Start
`uvicorn wmbus:app --port 8081 --workers 1` bzw.  
`uvicorn wmbus:app --port 8081 --workers 1 --host 0.0.0.0` zur Freigabe für alle Rechner im gleichen Netzwerk


#### Ausgabe
```
INFO:     Started server process [22444]
INFO:     Waiting for application startup.
INFO:     Application startup complete.
INFO:     Uvicorn running on http://127.0.0.1:8081 (Press CTRL+C to quit)
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

Über die o.g. Adresse kann der Webserver im Browser aufgerufen werden, z.B. http://IP-Adresse:8081.  

#### Laden von Testdaten
Über "Snapshot wählen..." können in der Web-Oberfläche auch Testdaten aus einem Snapshot geladen werden.  


# Auswertungs-Server
Der Auswertungsserver liest die Telegramme aus den Snapshots, entschlüsselt sie und bereit die Daten auf, z.B. für die Unterjährige Verbrauchsmitteilung (UVI). 

#### Start
`uvicorn main:app --port 8080` bzw.  
`uvicorn main:app --port 8080 --host 0.0.0.0`  zur Freigabe für alle Rechner im gleichen Netzwerk  

```
Warte auf Key auf Port 53165 …
INFO:     Started server process [23040]
INFO:     Waiting for application startup.
INFO:     Application startup complete.
INFO:     Uvicorn running on http://127.0.0.1:8080 (Press CTRL+C to quit)
Userfile nicht gefunden – erstelle neue Datei...
========================================
Admin-Token erzeugt:
0123456789abcdef
Bitte sicher notieren bzw. speichern!
========================================
```

#### Benutzereinrichtung
Über den o.g. Link und mit Hilfe des Admin-Tokens kann nun die Login-Seite aufgerufen werden. Anschließend können verschiedene Nutzer gespeichert werden. Dafür ist eine Wohnungsnummer und die zugehörige Fläche anzugeben.  
Nutzer werden über Token (z.B. db7c01267a874140) unterschieden und zu Wohnungen zugeteilt.  
Admin-Nutzer sind Nutzer ohne hinterlegte Wohnung.  

#### Konfiguration der Zählerplätze
Zählerplätze und Zähler können in einer Excel-Datei verwaltet werden. Zu jedem Zählerplatz können dort beliebig viele Zähler hinterlegt werden, so dass auch Zählerwechsel berücksichtigt werden können.  
Eine Beispiel-Datei findet sich unter Releases. 
Über den Daten-Import kann eine Excel-Datei importiert und mit einem Passwort verschlüsselt gespeichert werden. Das Passwort kann dem Auswertungsserver über unterschiedliche Methoden übergeben werden (siehe [locations.md](locations.md)).  

#### Testmodus
Sobald zusätzlich zum Admin ein Nutzer mit Wohnung = 1 angelegt ist und in der Datei cfg.ini der Parameter SnapshotDir noch auf tests/snapshots steht
```
# Verzeichnis für JSON-Snapshots
#SnapshotDir = snapshots
SnapshotDir = tests/snapshots
```
kann nun auch über die Benutzerverwaltung eine UVI mit den Testdaten erzeugt und angezeigt werden.  


#### Freigabe für andere Geräte im gleichen Netzwerk  
Mit den oben genannten Befehlen ist der Server nur von dem Gerät erreichbar, auf dem er läuft. Durch Anfügen von `--host 0.0.0.0` wird er für alle Geräte im gleichen Netzwerk erreichbar:  
`uvicorn main:app --port 8080 --host 0.0.0.0`  

---

# Autostart unter Linux
Eine Datei namens selbst-ableser.service anlegen  
`sudo nano /etc/systemd/system/selbst-ableser.service`  
und mit folgendem Inhalt befüllen:  

```
[Unit]
Description=Read wmbus meters
After=network.target  

[Service]  
User=pi
WorkingDirectory=/home/pi/selbst-ableser/
Environment="PATH=/home/pi/wmbus/venv/bin"
ExecStart=/home/pi/selbst-ableser/venv/bin/uvicorn wmbus:app --host 0.0.0.0 --port 8081 --workers 1
StandardOutput=null
StandardError=journal
Restart=always
RestartSec=10


[Install]
WantedBy=multi-user.target
```

Start the service and enable it to be started at every boot:  
`sudo systemctl daemon-reload`  
`sudo systemctl start selbst-ableser.service`  
`sudo systemctl enable selbst-ableser.service`  

Check status of service  
`sudo systemctl status selbst-ableser.service`  

Check, which services are enabled  
`systemctl list-unit-files --state=enabled`  
