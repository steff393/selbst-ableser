# selbst-ableser

**selbst-ableser** liest Funkzähler (wM-Bus / OMS) lokal aus, archiviert die verschlüsselten
Telegramme und wertet sie zu einer monatlichen Verbrauchsinformation (UVI) für Heizkostenverteiler,
Warmwasser- und Kaltwasserzähler aus.  

Das Projekt richtet sich an Eigentümer & Mieter, die ihre Verbrauchsdaten **selbst**, **lokal** und **datenschutzfreundlich** erfassen möchten – ohne Cloud-Zwang oder externe Dienste.  

Webseite: [selbst-ableser.de](https://www.selbst-ableser.de)

---

## Funktionen

* 📡 Empfang von Funkzählern nach **wM-Bus** / **OMS**
* 🐹 Vollständig in **Go** geschrieben — ein statisches Binary, keine Laufzeit-Abhängigkeiten
* 🍓 Für den Dauerbetrieb auf einem **Raspberry Pi** ausgelegt (SD-Karten-schonend)
* 🧮 Verbrauchsberechnung inkl. HKV-Stichtagsreset, kc-Faktoren, Warmwasser-/Kaltwasserzählern
* 🌐 Weboberfläche für Zugriff, Auswertung und Betriebsparameter
* 🧩 Modular erweiterbar (weitere Zählertypen, Ausgaben, Schnittstellen)
* 🏠 Ein oder mehrere Empfänger, ein oder mehrere Geräte — vom Einzelhaushalt bis zur
  räumlich verteilten Anlage

---

## Architektur in Kürze

Zwei eigenständige Go-Module, ausschließlich über eine authentifizierte Netzwerkschnittstelle
gekoppelt — keine gemeinsame Datenbank, kein gemeinsamer Dateizugriff:

* **Collector** — empfängt Telegramme, prüft Rahmen, meldet sie an den Evaluator. Besitzt
  strukturell kein Schlüsselmaterial, keine Stammdaten, keine Zugangsdaten.
* **Evaluator** — entschlüsselt das Archiv, berechnet Verbräuche, stellt die Weboberfläche
  bereit und verwaltet die Betriebsparameter jedes Collectors.


Details, Bedrohungsmodell und Entwurfsentscheidungen: [docs/architektur.md](docs/architektur.md).

---

## Voraussetzungen

* Go **1.26+** (alternativ: fertige Binaries, siehe Releases)
* Unterstützter wM-Bus-Funkadapter: [iU891A-XL](https://shop.imst.de/wireless-solutions/usb-radio-products/89/bundle-iu891a-xl-wireless-m-bus-usb-adapter-868-mhz-w.-antenna)
* Windows oder Linux 

---

## Installation und erste Schritte

[Schnellstart Windows](docs/quickstart.md)

---

## Sicherheit & Datenschutz

**selbst-ableser** verfolgt einen klaren Ansatz:

* 📉 Datensparsamkeit — keine personenbezogenen Daten nötig für Erfassung und Auswertung
* 🏠 Lokale Verarbeitung, keine automatische Cloud-Anbindung
* 🔐 Volle Kontrolle über die eigenen Messdaten

Ideal für private Anwender, Vermieter oder Bastler, die ihre Zählerdaten selbst auslesen möchten.  

---

## Screenshots
<img alt="Live-Ansicht" src="https://private-user-images.githubusercontent.com/73853447/646801085-b4d5f4c3-23cf-41e0-bedc-1051e5075612.png?jwt=eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9.eyJpc3MiOiJnaXRodWIuY29tIiwiYXVkIjoicmF3LmdpdGh1YnVzZXJjb250ZW50LmNvbSIsImtleSI6ImtleTUiLCJleHAiOjE3ODg2NDA5NTYsIm5iZiI6MTc4ODY0MDY1NiwicGF0aCI6Ii83Mzg1MzQ0Ny82NDY4MDEwODUtYjRkNWY0YzMtMjNjZi00MWUwLWJlZGMtMTA1MWU1MDc1NjEyLnBuZz9YLUFtei1BbGdvcml0aG09QVdTNC1ITUFDLVNIQTI1NiZYLUFtei1DcmVkZW50aWFsPUFLSUFWQ09EWUxTQTUzUFFLNFpBJTJGMjAyNjA5MDUlMkZ1cy1lYXN0LTElMkZzMyUyRmF3czRfcmVxdWVzdCZYLUFtei1EYXRlPTIwMjYwOTA1VDIwMzczNlomWC1BbXotRXhwaXJlcz0zMDAmWC1BbXotU2lnbmF0dXJlPTI0MzA4NDQyZGM1ZmNhMzRiMGM3YTg0YzA5NTViZWZiODM2NzQ3NmU3Yzk5ZDkzNmFhYTQ5MTY4MjFmOTc0YWEmWC1BbXotU2lnbmVkSGVhZGVycz1ob3N0JnJlc3BvbnNlLWNvbnRlbnQtdHlwZT1pbWFnZSUyRnBuZyJ9.0D9aZTl0DQQjk5IBocMcDOFEDwuY7bTLyVFe0RO9CmM" />
<br><br>
<img alt="Übersicht" src="https://private-user-images.githubusercontent.com/73853447/646800962-1cb33c9c-c705-4ccb-9c01-507031eb98f3.png?jwt=eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9.eyJpc3MiOiJnaXRodWIuY29tIiwiYXVkIjoicmF3LmdpdGh1YnVzZXJjb250ZW50LmNvbSIsImtleSI6ImtleTUiLCJleHAiOjE3ODg2NDA5OTEsIm5iZiI6MTc4ODY0MDY5MSwicGF0aCI6Ii83Mzg1MzQ0Ny82NDY4MDA5NjItMWNiMzNjOWMtYzcwNS00Y2NiLTljMDEtNTA3MDMxZWI5OGYzLnBuZz9YLUFtei1BbGdvcml0aG09QVdTNC1ITUFDLVNIQTI1NiZYLUFtei1DcmVkZW50aWFsPUFLSUFWQ09EWUxTQTUzUFFLNFpBJTJGMjAyNjA5MDUlMkZ1cy1lYXN0LTElMkZzMyUyRmF3czRfcmVxdWVzdCZYLUFtei1EYXRlPTIwMjYwOTA1VDIwMzgxMVomWC1BbXotRXhwaXJlcz0zMDAmWC1BbXotU2lnbmF0dXJlPTY4OWYyYWZlZDMzY2ViNzc1YjY0MDRmODZjYzQ5ODZkMzRmMjQxNDY0MWU1OWQ3YTAyMmJhNTQ1YmM2MzMzZTkmWC1BbXotU2lnbmVkSGVhZGVycz1ob3N0JnJlc3BvbnNlLWNvbnRlbnQtdHlwZT1pbWFnZSUyRnBuZyJ9.jyccSlCSbZX2qhhkR5ZrvtqiICN1m9J9U4Y2JjzxE34" />

---

## Projektstatus

🚧 **In Entwicklung** — vollständig neu geschrieben in Go (vormals Python-Prototyp).
Funktionen und Schnittstellen können sich noch ändern.

---

## Lizenz

Siehe [LICENSE](LICENSE): freie Nutzung für private, nicht-kommerzielle Zwecke. Für
kommerzielle Nutzung ist eine gesonderte Lizenz beim Autor erforderlich.

---

## Name

**selbst-ableser** – weil deine Daten dir gehören.
