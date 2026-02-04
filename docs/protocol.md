## Hardware
USB-Dongle: ui891a-xl (Hersteller: imst)  
Antenne:  ANT-868-CW-HWR-SMA (Hersteller: TE Connetivity / Linux Technologies)

## Protokollbeschreibung
Ein typisches Telegramm sieht wie folgt aus:  
```c++
C0 09 20 10 86 6E 38 03 FF 02 A0 
00 01 02 03 04 05 06 07 08 09 10
|----------- Dongle -----------| 

32 44 68 50 06 32 76 42 69 80 A0 11 
11 12 13 14 15 16 17 18 19 20 21 22
|-----------wmBus-Header----------|

FF 32 9B 06 10 03 20 01 81 07 88 07 0C 37 00 37 33 3B 53 09 0E 04 01 00 00 00 00 00 00 00 06 05 04 6F 4A 64 65 93 F0 F5 89 C0
|----------------- Payload ----------------------------------------------------------------------------------------| |CRC| End
23 24 25 26 27 28 29 30 31 32 33 34 35 36 37 38 39 40 41 42 43 44 45 46 47 48 49 50 51 52 53 54 55 56 57 58 59 60 61 62 63 64
```

Weitere Infos: [SLIP-Protokoll](https://de.wikipedia.org/wiki/Serial_Line_Internet_Protocol)  
Escape-Sequenzen möglich, s. Wiki  
CRC-Berechnung s.u.  

### Teil 1: Receiver Infos (Dongle)
```c++
#C0 09 20 10 86 6E 38 03 FF 02 A0 
#00 01 02 03 04 05 06 07 08 09 10 

Byte 0: Start Byte  
Byte 1: Endpoint WMBUS (in Stick)
Byte 2: SAP (Service Access Point), 0x20 = Radio Link
Byte 3: ? Message ID, Befehlstyp, 0x10 = Radio Packet Received
Byte 4-7: ? Zeitstempel, Byte 4 wird mit jeder Sekunde erhöht
Byte 8/9: ?
Byte 10: RSSI, 0xA0 = -96dBm
```

### Teil 2: wmBus-Telegramm
```c++
32 44 68 50 06 32 76 42 69 80 A0 11 FF 32 9B 06 10 03 20 01 81 07 88 07 0C 37 00 37 33 3B 53 09 0E 04 01 00 00 00 00 00 00 00 06 05 04 6F 4A 64 65 93 F0
00 01 02 03 04 05 06 07 08 09 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30 31 32 33 34 35 36 37 38 39 40 41 42 43 44 45 46 47 48 49 50

Header:
Byte 0:   L = Length, 0x32 = 50 Byte, die nach L kommen
Byte 1:   C = Control, 0x44?
Byte 2/3: M = Manufacturer ID
Byte 4-7: A-> D = Device ID
Byte 8:   A-> VER = Version
Byte 9:   A-> TYP = medium, 0x80 = Heat Cost Allocator
Byte 10:  CI = Control Information 0xA0
Byte 11:  ACC = Access Number

Payload, Beispiel Techem HKV:
Byte 12/13: Altes Datum
Byte 14/15: Alter Zählerstand
Byte 16/17: Aktuelles Datum
Byte 18/19: Aktueller Zählerstand
Byte 20/21: Raumtemperatur
Byte 22/23: Heizkörpertemperatur
Byte 24-49: ?
```

### Teil 3: CRC 
```c++
F5 89 
62 63 
```
16-Bit-CRC-Prüfsumme nach der CRC-16/CCITT (X-25) Variante über den Inhalt zwischen C0 ... C0 (ohne CRC).  
Initialwert: 0xFFFF  
Polynom: 0x8408 (reflektiert, LSB-first)  
Am Ende wird das Ergebnis invertiert (Final XOR 0xFFFF), wie im X-25 Standard üblich.  
Im Telegramm wird auch noch die Reihenfolge der beiden Byte gedreht (little/big endian).  


### Teil 4: Ende des Dongles
```c++
C0
64
```


## Dongle-Initialisierung
### Wakeup:
30x C0:  
```c++
C0C0C0C0C0C0C0C0C0C0C0C0C0C0C0C0C0C0C0C0C0C0C0C0C0C0C0C0C0C0
```

### 10 03 Device Info
Byte 0: C0 Start  
Byte 2/3: 01 03 Kommando  
Byte 4/5: 04 24 CRC  
Byte 6: C0 Ende  
```c++
Anfrage: 
C0 01 03 04 24 C0
Antwort:
C0 01 04 00 6E48090000BE2D0600C70B0000 CF 5D C0
         OK |------ Antwort ---------| |CRC|
              |--UID-|
```
UID = 00000948

### 09 81 Get Radio Address
```c++
Anfrage: 
C0 09 81 DE4D C0
Antwort:
C0 09 82 00 B32500203015010E 2043 C0
```

### 09 01 Get Configuration
```c++
Anfrage: 
C0 09 01 D6C9 C0
Antwort:
C0 09 02 00 00 0E 00   00 00 32 00 A0 BB 0D 00 65CC C0
         OK LM Options        LED  Recalibrate
```
00: LinkMode (Hier noch 00, also vermutlich "None" oder Standard)  
0E 00: Options (Flags für RCV_IND, SND_IND und RCV_ALL)    
32 00: LED-Flash Zeit (50ms)  
A0 BB 0D 00: Recalibrate-Intervall (000DBBA0 = 900.000 ms = 15 Minuten)  


### 09 03 Set Configuration
```c++
Anfrage:
C0 09 03 030E0000003200A0BB0D00 AF61 C0
Antwort:
C0 09 04 00 B23D C0
```
03: WMBus Mode Stellt den Stick auf den kombinierten C1 & T1 Modus ein (868.95 MHz)  
0E 00:	Options	Bitmaske für das Verhalten: RCV_IND (0x02), SND_IND (0x04) und RCV_ALL (0x08). Addiert ergibt das 0E. Damit meldet der Stick jedes empfangene Paket sofort an den PC.  
00 00:	UI Options	Meist ungenutzt für interne Display- oder User-Interface-Optionen.  
32 00:	LED Control	Definiert, wie lange die LED am Stick bei Funkempfang blinken soll (hier: 0x32 = 50 Millisekunden).  
A0 BB 0D 00:	Recalibration	Das Intervall für die automatische Frequenz-Kalibrierung (hier: 900.000 ms = 15 Minuten). Wichtig, damit der Empfang stabil bleibt.  
