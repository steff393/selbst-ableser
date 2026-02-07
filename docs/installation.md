# Anleitung
Git-Repository klonen:  
`git clone https://github.com/steff393/selbst-ableser.git`  

Virtual Environment venv anlegen, aktivieren und Abhängigkeiten installieren:  
`cd selbst-ableser`  
`python -m venv venv`  
`source venv/bin/activate` (Linux) bzw. `venv\Scripts\activate` (Windows)  
`pip install -r requirements.txt`  

Nun können entweder der wM-Bus-Server oder der Auswertungs-Server gestartet werden.  

## wM-Bus-Server
Der wM-Bus-Server empfängt Telegramme und stellt sie auf einer Webseite (noch verschlüsselt dar). Außerdem speichert er täglich (oder monatlich) sogenannte Snapshots ab. Diese enthalten das zuletzt empfangene Telegramm jedes Zählers.  

#### Vorbereitung
In die Datei cfg.ini muss als Port der COM-Port des iU891A-XL-Empfängers eingetragen werden (oder Port auskommentiert werden, falls testweise nur mit Snapshots gearbeitet wird).  
Die AES-Schlüssel der Zähler sind für diesen Schritt nicht erforderlich.  

#### Start
`python wmbus.py`  

Alternativ kann auch ein bestehender Snapshot mit Testdaten eingelesen werden.  
`python wmbus.py -snap 2026-01-31.json` 

#### Ausgabe
```
Verbinde mit iU891A-XL an COM5...
HTTP-Server aktiv: http://192.168.178.76:8080
-> C001030424C0 Get Device Info
<- C00104006E48090000BE2D0600C70B0000CF5DC0 Device Info OK
-> C00903030E0000003200A0BB0D00AF61C0 Set Configuration C1/T1
<- C0090400B23DC0 Configuration OK
Stick ist bereit und empfängt... (Strg+C zum Beenden)
12:53:38 ✔ Zähler 42130857 | RSSI -103 dBm | wmBus 32446850570813426980A011FF3249027004CE0170068E06204900496346373A1C1A1500000000000000000000001017143C40
Telegramm von 42130857 blockiert
12:53:42 ✔ Zähler 42130855 | RSSI -95 dBm | wmBus 32446850550813426980A011FF32B80270044F03A108820A177F007F80ADA95024303D02000000000000000000021033644974
```

Über die o.g. Adresse kann der Webserver im Browser aufgerufen werden.  

## Auswertungs-Server
Der Auswertungsserver liest die Telegramme aus den Snapshots, entschlüsselt sie und bereit die Daten auf, z.B. für die Unterjährige Verbrauchsmitteilung (UVI). 

#### Start
`python main.py`  

```
Warte auf Key auf Port 53165 …
HTTP-Server aktiv: http://192.168.178.76:8080
```

#### Benutzereinrichtung
Über den o.g. Link muss nun zuerst ein Admin-Token gespeichert werden. Anschließend können verschiedene Nutzer gespeichert werden.
Nutzer werden über Token (z.B. db7c01267a874140) unterschieden und zu Wohnungen zugeteilt.  
Admin-Nutzer sind Nutzer ohne hinterlegte Wohnung. 

#### Konfiguration der Zählerplätze
Zählerplätze und Zähler können in einer Excel-Datei verwaltet werden. Zu jedem Zählerplatz können dort beliebig viele Zähler hinterlegt werden, so dass auch Zählerwechsel berücksichtigt werden können.  
Eine Beispiel-Datei findet sich unter Releases. 
Über http://192.168.178.76:8080/admin-token/import.html kann eine Excel-Datei importiert und mit einem Passwort verschlüsselt gespeichert werden. Das Passwort kann dem Auswertungsserver als Parameter beim Aufruf übergeben werden.  

`python main.py -pw test_passwort`  
```
Registry entschlüsselt
HTTP-Server aktiv: http://192.168.178.76:8080
```

#### Testmodus
Sobald zusätzlich zum Admin ein Nutzer mit Wohnung = 1 angelegt ist und in der Datei cfg.ini der Parameter SnapshotDir noch auf tests\snapshots steht
```
# Verzeichnis für JSON-Snapshots
#SnapshotDir = snapshots
SnapshotDir = tests\snapshots
```
kann nun auch über die Benutzerverwaltung eine UVI mit den Testdaten erzeugt werden:  
http://192.168.178.76:8080/token/uvi.html

---

# Autostart unter Linux
Create a file selbst-ableser.service  
`sudo nano /etc/systemd/system/selbst-ableser.service`  

```
[Unit]
Description=Read wmbus meters
After=network.target  

[Service]  
ExecStart=/home/sf/selbst-ableser/venv/bin/python /home/sf/selbst-ableser/main.py  
WorkingDirectory=/home/sf/selbst-ableser/
StandardOutput=inherit
StandardError=inherit
Restart=always
RestartSec=10
User=sf

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
