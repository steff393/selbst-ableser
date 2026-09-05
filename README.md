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
