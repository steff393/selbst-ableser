# Schnellstart

## 1. Herunterladen und entpacken

Das aktuelle Windows-Release von der [Releases-Seite](../../../releases) herunterladen und in
einen beliebigen Ordner entpacken. Darin liegen `saEvaluator.exe`, `saCollector.exe` und der
Ordner `test` mit einem fertigen, frei erfundenen Testhaus (7 Wohnungen, 56 Zähler).

## 2. Evaluator mit den Testdaten starten

`cmd.exe` in diesem Ordner öffnen und:

```cmd
saEvaluator.exe test
```

Der Ordnername ist das einzige Argument: `test` ist die mitgelieferte Testanlage. Ein anderer
Ordner wäre eine andere, völlig getrennte Anlage — mehr steckt hinter dem Wechsel zwischen
Test- und echten Daten nicht.

Das Fenster bleibt offen, solange der Evaluator laufen soll. Im Browser: **http://localhost:8226**  

Anmelden mit dem Betreiber-Passwort:

```
TEST-TEST-TEST-TEST
```

## 3. UVI der Testdaten ansehen

Oben rechts auf das Menü-Symbol (☰) klicken, dann **UVI** — zeigt die Verbrauchsauswertung des
Testhauses, wohnungsweise und über die Zeit navigierbar.

## 4. Optional: eigenen wM-Bus-Stick anschließen und Live-Telegramme ansehen

Nur mit einem [iU891A-XL](https://shop.imst.de/wireless-solutions/usb-radio-products/89/bundle-iu891a-xl-wireless-m-bus-usb-adapter-868-mhz-w.-antenna)
(oder kompatiblen) USB-Empfänger möglich — sonst diesen Schritt einfach überspringen.

Empfänger einstecken, dann im Menü (☰) auf **Live-Ansicht** gehen und **Live-Ansicht starten**
klicken. Anschließend in einem **zweiten** `cmd.exe`-Fenster, im selben Ordner:

```cmd
saCollector.exe
```

Empfangene Telegramme erscheinen innerhalb weniger Sekunden in der Live-Ansicht.

## Beenden

In beiden Fenstern `Strg+C`. 
