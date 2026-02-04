# Security-Konzept

Dieses Dokument beschreibt die sicherheitsrelevanten Überlegungen und Schutzmaßnahmen des Projekts.  
Ziel ist es, sensible Daten bestmöglich zu schützen und gleichzeitig eine praxisnahe Nutzung zu ermöglichen.

---

## Überblick über zu schützende Daten

### Personenbezogene Daten
Personenbezogene Daten (Name, Anschrift etc.) sind für die technische Erfassung und Auswertung der Verbrauchsdaten **nicht erforderlich**.  
Das System ist daher so konzipiert, dass **keine personenbezogenen Daten erfasst oder gespeichert werden** (Datensparsamkeit nach DSGVO).

---

### AES-Schlüssel der Zähler (kritischste Daten)
Die AES-Schlüssel der Zähler sind die **sensibelsten Daten im System**.

**Begründung:**
- Die Schlüssel können bei Offenlegung **nicht geändert** werden.
- Ein kompromittierter Schlüssel ermöglicht jederzeit den Zugriff auf **sämtliche historischen und zukünftigen Zählerdaten**.
- Ein Missbrauch ist nicht zeitlich oder räumlich begrenzt.

**Schutzbedarf: sehr hoch**

**Maßnahmen:**
- AES-Schlüssel sollten ausschließlich dem Gebäudeeigentümer bzw. Verwalter bekannt sein.
- Speicherung nur **verschlüsselt** (z. B. im Locationfile).
- Optional zusätzliches Backup **ausschließlich auf privaten, vertrauenswürdigen Systemen**.
- Keine Speicherung im Klartext, weder persistent noch im Quellcode.

---

### Benutzer-Token
Benutzer-Token erlauben den Zugriff auf Auswertungen.

**Eigenschaften:**
- Zugriff nur auf **aggregierte Verbrauchsdaten**, keine Live-Daten.
- Dennoch personenbezogen, da sie individuelle Verbrauchsinformationen enthalten.
- Token enthalten zusätzlich **Ein- und Auszugsdaten**, um den Zugriff zeitlich auf die Mietdauer zu begrenzen.
- Token können bei Verlust **widerrufen und neu erstellt** werden.

**Schutzbedarf: mittel**

**Maßnahmen:**
- Speicherung ausschließlich **verschlüsselt**.
- Trennung von Token und AES-Schlüsseln.
- Möglichkeit zum gezielten Widerruf einzelner Token.

---

### Auswertungen (z. B. UVI, Jahresübersichten)
Auswertungen enthalten personenbezogene Verbrauchsdaten und sind daher schützenswert.

**Eigenschaften:**
- Auswertungen werden **nicht persistent gespeichert**.
- Sie werden bei jedem Zugriff **on-the-fly** aus den zugrunde liegenden Daten erzeugt.
- Nutzer/Mieter können die Auswertungen selbst **drucken oder exportieren**.

**Verantwortungsübergang:**
- Mit dem Export (z. B. als PDF) geht die Verantwortung für den Schutz dieser Dateien auf den jeweiligen Nutzer über.

---

### Zähler-Eigenschaften
Beispiele: Wohnung, Raum, Zählertyp.

**Bewertung:**
- Für sich genommen eher unkritisch.
- In Kombination mit anderen Daten jedoch potenziell sensibel.

**Maßnahmen:**
- Speicherung gemeinsam mit den AES-Schlüsseln.
- Daher ebenfalls **verschlüsselt abgelegt**.

---

### wM-Bus-Telegramme / Snapshots
Zähler senden ihre Daten als **verschlüsselte wM-Bus-Telegramme**.

**Bewertung:**
- Der Empfang der verschlüsselten Telegramme ist prinzipiell für jeden mit geeignetem Empfänger möglich.
- Die Telegramme selbst enthalten **keine verwertbaren Informationen ohne AES-Schlüssel**.

**Maßnahmen:**
- Verschlüsselte Telegramme müssen nicht zusätzlich geschützt werden.
- Sie können unverschlüsselt als Backup auf unterschiedlichen Datenträgern gespeichert werden.
- **Entschlüsselte Telegramme oder Klartext-Zählerstände werden nicht gespeichert**, sondern ausschließlich bei Bedarf temporär im Arbeitsspeicher erzeugt.

---

### Programmkonfiguration (`cfg.ini`)
Die Konfigurationsdatei enthält keine sensiblen Daten.

**Bewertung:**
- Unkritisch.

**Empfehlung (optional):**
- Schreibrechte einschränken, um Manipulationen zu vermeiden.

---

## Betriebskonzepte

### Variante 1: Komplett lokal auf einem Raspberry Pi
**Beschreibung:**  
Das Programm läuft vollständig auf einem Raspberry Pi, an dem auch der wM-Bus-Empfänger angeschlossen ist.

**Vorteile:**
- Daten verlassen das Gebäude nicht.
- Keine Internet-Erreichbarkeit erforderlich.

**Nachteile:**
- Raspberry Pi ist ggf. physisch zugänglich.
- AES-Schlüssel müssen auf dem Gerät gespeichert sein.
- Speicherkarte ist prinzipiell auslesbar.
- Mieter/Nutzer müssen sich ins lokale Netzwerk einwählen.
- Backups nur manuell (z. B. USB-Stick).

**Empfehlungen:**
- AES-Schlüssel im Locationfile **verschlüsselt** ablegen.
- Entschlüsselung nur nach **manueller Passworteingabe beim Start**.
- Regelmäßige Offline-Backups durchführen.

---

### Variante 2: Raspberry Pi für Erfassung, VPS für Auswertung
**Beschreibung:**  
Der Raspberry Pi sammelt die verschlüsselten Telegramme und überträgt sie regelmäßig (z. B. täglich) auf einen VPS.  
Die Auswertung erfolgt auf dem Server.

**Vorteile:**
- Raspberry Pi benötigt keinen besonderen Schutz.
- Komfortabler Zugriff für Mieter/Nutzer über das Internet.
- Redundante Datenspeicherung (lokal + VPS).

**Nachteile:**
- AES-Schlüssel müssen auf dem VPS verfügbar sein.
- Bei Kompromittierung des Servers ist ein **weltweiter Zugriff** auf die Daten möglich.

**Empfehlungen:**
- AES-Schlüssel auch hier **verschlüsselt** speichern.
- Manuelle Passworteingabe nach Server-Neustart.
- VPS regelmäßig aktualisieren und absichern:
  - Firewall
  - Minimalprinzip
  - SSH-Hardening
- Zugriff auf Auswertungen strikt tokenbasiert.

---

### Variante 3: Raspberry Pi für Erfassung, semiautomatische Auswertung
**Beschreibung:**  
Der Raspberry Pi speichert ausschließlich die verschlüsselten Telegramme.  
Der Verwalter lädt diese monatlich auf einen lokalen Rechner und erstellt dort die Auswertungen.

**Vorteile:**
- Höchstes Sicherheitsniveau.
- AES-Schlüssel verbleiben ausschließlich auf dem Rechner des Verwalters.
- Raspberry Pi benötigt keinerlei Schutzmaßnahmen.
- Regelmäßige, kontrollierte Backups.
- Keine dauerhafte Internetanbindung erforderlich.

**Nachteile:**
- Manueller Aufwand erforderlich.

**Empfehlungen:**
- Übertragung der Reports an Mieter datenschutzkonform gestalten  
  (z. B. verschlüsselte PDFs, getrennte Passwortübermittlung).
- Lokale Systeme des Verwalters entsprechend absichern (Passwort, Verschlüsselung, Backups).

---

## Ergänzende Sicherheitsprinzipien

- **Datensparsamkeit:**  
  Es werden nur technisch notwendige Daten verarbeitet.
- **Trennung von Zuständigkeiten:**  
  Telegramme, Schlüssel, Token und Auswertungen sind logisch getrennt.
- **Fail-safe-Design:**  
  Ohne AES-Schlüssel sind die gespeicherten Daten wertlos.
- **Kein Vendor-Lock-in:**  
  Alle Daten bleiben unter Kontrolle des Betreibers.

---

## Threat Model

### Angreiferprofile

1. **Neugieriger Dritter**
   - Empfang von wM-Bus-Telegrammen aus der Nähe des Gebäudes
   - Kein Zugriff auf AES-Schlüssel

2. **Lokaler Angreifer**
   - Physischer Zugriff auf den Raspberry Pi oder dessen Speicherkarte
   - Keine oder eingeschränkte Systemkenntnisse

3. **Externer Angreifer**
   - Netzwerkbasierter Angriff (z. B. auf VPS oder Weboberfläche)
   - Kein physischer Zugriff

4. **Insider**
   - Berechtigter Nutzer mit Token-Zugriff
   - Versuch der Einsicht in fremde Verbrauchsdaten

---

### Bedrohungen und Gegenmaßnahmen

| Bedrohung | Auswirkung | Gegenmaßnahme |
|----------|------------|---------------|
| Abhören von wM-Bus-Telegrammen | Keine verwertbaren Daten | AES-Verschlüsselung der Zähler |
| Diebstahl der SD-Karte | Zugriff auf Rohdaten | Keine Klartextdaten, Schlüssel verschlüsselt |
| Kompromittierung eines VPS | Zugriff auf Auswertungen | Verschlüsselte Schlüssel, Token-Trennung |
| Token-Leak | Zugriff auf Auswertungen | Token zeitlich begrenzt, widerrufbar |
| Insider-Zugriff | Datenschutzverletzung | Tokenbindung an Mietzeitraum |

---

### Restrisiken

- Kompromittierung des Verwalter-PCs kann zur Offenlegung der AES-Schlüssel führen.
- Verlust exportierter Reports liegt in der Verantwortung der Nutzer.
- Physischer Diebstahl aktiver, entschlüsselter Systeme während des Betriebs kann nicht vollständig verhindert werden.

Diese Restrisiken werden bewusst akzeptiert, da sie außerhalb des technisch sinnvoll beherrschbaren Rahmens liegen.

---
