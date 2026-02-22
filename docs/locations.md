# Entschlüsselung der Zählerplatzdaten

Die Datei `locations.json.enc` enthält die verschlüsselten Zählerplatzdaten auch die kritischen AES-Schlüssel der Funkzähler. Die AES-Schlüssel der Zähler sind die **sensibelsten Daten im System**. Um sie zu nutzen, muss dem Programm beim Start oder zur Laufzeit ein Passwort (Token) übergeben werden. Dafür stehen drei Methoden zur Verfügung.

---

## Methode A – Umgebungsvariable

Das Passwort wird vor dem Programmstart als Umgebungsvariable gesetzt. Das Programm liest es beim Start automatisch aus und entschlüsselt die Datei sofort.

**Windows (Eingabeaufforderung):**
```cmd
set LOCATION_PW=geheim
uvicorn main:app --port 8080
```

**Linux / macOS:**
```bash
LOCATION_PW=geheim uvicorn main:app --port 8080
```

**Wann geeignet?**
Wenn das System automatisiert startet (z. B. als Dienst oder per Skript) und das Passwort sicher in der Umgebung hinterlegt werden kann. Der Vorteil ist, dass kein manueller Eingriff nach dem Start nötig ist.  
Risiko: Das Passwort steht im Klartext in der Shell-History oder in Skriptdateien.

---

## Methode B – Socket-Verbindung

Das Programm startet zunächst ohne Passwort und wartet an einem konfigurierbaren Port (Standard: `53165`, konfigurierbar über `KeyPort`) auf eine eingehende Verbindung. Erst wenn das Passwort über diesen Kanal übermittelt wird, entschlüsselt das Programm die Datei und setzt die Verarbeitung fort.

Das Passwort wird dabei **nicht** als GET-Parameter übergeben (was in Logs landen würde), sondern als POST-Body bzw. direkt über den Socket.

**Variante 1 – curl (verhindert Eintrag in der Shell-History durch POST):**
```bash
curl -X POST --data-binary @- http://localhost:53165
# Passwort eingeben, dann abschicken mit:
# Strg-D (Linux/macOS) oder Strg-Z + Enter (Windows)
```

**Variante 2 – netcat, interaktiv:**
```bash
nc 127.0.0.1 53165
# Passwort eingeben, dann Strg-D oder Strg-C + Enter
```

**Variante 3 – netcat, nicht-interaktiv:**
```bash
 echo -n "geheim" | nc 127.0.0.1 53165
# Wichtig: Leerzeichen vor echo, damit es nicht in der History geloggt wird.
```
> **Hinweis:** Das führende Leerzeichen vor `echo` verhindert bei den meisten Shells (bash, zsh), dass der Befehl in der History gespeichert wird.

**Wann geeignet?**
Wenn das Passwort nicht persistent gespeichert werden soll und der Betreiber es bewusst nach jedem Neustart manuell eingeben möchte. Das Programm ist solange nicht voll funktionsfähig, bis das Passwort übermittelt wurde – was in manchen Szenarien ein gewünschtes Sicherheitsmerkmal ist.

---

## Methode C – HTTP POST über die Web-Oberfläche

Das Passwort wird über einen HTTP POST-Request an `/locations` übermittelt. Dies entspricht dem Token-Panel in der Web-Oberfläche, das den Schlüssel direkt aus dem Browser an den Server sendet.

```http
POST /locations
Content-Type: application/json

{ "key": "geheim" }
```

Der aktuelle Sperrstatus kann jederzeit abgefragt werden:

```http
GET /locations/locked
```

Antwort: `{"status":"unlocked"}` oder `{"status":"locked"}`

**Wann geeignet?**
Wenn die Web-Oberfläche ohnehin geöffnet ist und das Passwort bequem über den Browser eingegeben werden soll – ohne Zugriff auf die Kommandozeile des Servers. Der Token kann dabei im Browser-LocalStorage gespeichert und bei Bedarf erneut gesendet werden. Hierbei muss sichergestellt werden, dass niemand unbefugt Zugriff auf den Browser hat.  

---

## Vergleich auf einen Blick

| | A – Umgebungsvariable | B – Socket | C – HTTP POST |
|---|---|---|---|
| Entschlüsselung | Beim Start | Nach manueller Eingabe | Nach manueller Eingabe |
| Passwort im Klartext | ⚠️ In Shell/Skript | ✅ Nein | ✅ Nein |
| Benötigt Kommandozeile | ✅ Ja | ✅ Ja | ❌ Nein |

Empfohlen wird die Variante B. Die anderen beiden Varianten können gewählt werden, wenn das Risiko verstanden wurde und durch entsprechende Zusatzmaßnahmen verringert wurde.  
