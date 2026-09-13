// Command receiverdump is a one-shot diagnostic for an unknown wM-Bus
// receiver: it listens on a serial port at several candidate baud rates,
// records everything that arrives, and writes a single report file meant
// to be sent back to whoever is adding support for that device.
//
// It exists because saCollector's own receiver support is written for one
// specific device (the IMST iU891A-XL: SLIP framing, a vendor handshake,
// 115200 baud) and adapting it to another one — an Innotas box built
// around a Radiocrafts RC1180-MBUS3, say — needs three facts the vendor
// documentation usually does not state: which baud rate the module talks
// at, whether it streams frames on its own or has to be configured first,
// and what a frame looks like on the wire.
//
// Deliberately built for a single run with no interaction: every baud
// rate is tried in one pass, and the report is self-contained. The
// alternative — asking someone to re-run with different settings — costs
// a round trip per attempt, which is the actual expense when the person
// with the hardware is reachable only by email.
//
// It only ever *reads*, with one exception: the config-mode probe sends
// a single 0x00 byte (which is how a Radiocrafts module is asked to open
// its command interface) and then 'X' (which closes it again). Neither
// writes to the device's configuration memory, and a power cycle undoes
// anything either could have changed. Pass -no-probe to skip even that.
package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"go.bug.st/serial"
	"go.bug.st/serial/enumerator"

	"selbst-ableser/collector/internal/telegram"
)

// dumpLimit bounds how much of each capture ends up in the report as a
// hexdump. Enough to see the structure of several frames, small enough
// that the whole file still goes through as a mail attachment; the
// analysis above it always covers everything that was captured, not just
// this much.
const dumpLimit = 4096

func main() {
	port := flag.String("port", "", "serial device path, e.g. /dev/ttyUSB0; empty picks the only USB serial port present, or lists them")
	secondsPerBaud := flag.Int("seconds", 60, "how long to listen at each baud rate")
	baudList := flag.String("bauds", "19200,9600,38400,57600,115200", "candidate baud rates, in the order they are tried")
	outPath := flag.String("out", "receiver-dump.txt", "report file to write")
	noProbe := flag.Bool("no-probe", false, "skip the config-mode probe (send nothing at all to the device)")
	flag.Parse()

	if err := run(*port, *secondsPerBaud, *baudList, *outPath, !*noProbe); err != nil {
		fmt.Fprintln(os.Stderr, "\nreceiverdump:", err)
		os.Exit(1)
	}
}

func run(portPath string, secondsPerBaud int, baudList, outPath string, probe bool) error {
	bauds, err := parseBauds(baudList)
	if err != nil {
		return err
	}

	report := &strings.Builder{}
	fmt.Fprintf(report, "receiverdump — %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(report, "System: %s/%s, Go %s\n\n", runtime.GOOS, runtime.GOARCH, runtime.Version())

	portPath, err = resolvePort(portPath, report)
	if err != nil {
		return err
	}

	total := time.Duration(len(bauds)*secondsPerBaud) * time.Second
	fmt.Printf("Port: %s\n", portPath)
	fmt.Printf("Baudraten: %s\n", baudList)
	fmt.Printf("Dauer: %d s je Baudrate, insgesamt etwa %s.\n", secondsPerBaud, total.Round(time.Second))
	if probe {
		fmt.Println("Nach jedem Mithören wird einmal 0x00 gesendet (Konfigurationsmodus öffnen) und danach 'X' (wieder schließen).")
	}
	fmt.Println("\nBitte laufen lassen, bis \"fertig\" erscheint — Zähler senden nur alle paar Minuten.")
	fmt.Println()

	opened := 0
	var lastOpenErr error
	for _, baud := range bauds {
		fmt.Printf("== %d Baud ==\n", baud)
		captured, probeResp, err := listenAt(portPath, baud, time.Duration(secondsPerBaud)*time.Second, probe)
		if err != nil {
			lastOpenErr = err
			fmt.Printf("   Fehler: %v\n\n", err)
			fmt.Fprintf(report, "== %d Baud ==\nFehler beim Öffnen/Lesen: %v\n\n", baud, err)
			continue
		}
		opened++
		fmt.Printf("   %d Bytes empfangen%s\n\n", len(captured), probeSummary(probeResp))
		writeBaudSection(report, baud, captured, probeResp)
	}

	if err := os.WriteFile(outPath, []byte(report.String()), 0o644); err != nil {
		return fmt.Errorf("Bericht schreiben: %w", err)
	}

	// Nothing was measured at all, so the report holds no answer — say so
	// with a non-zero exit rather than the reassuring "fertig" below,
	// which would otherwise be the only thing reported back.
	if opened == 0 {
		return fmt.Errorf("der Port ließ sich bei keiner Baudrate öffnen — es wurde nichts gemessen.\n"+
			"    Letzter Fehler: %v\n"+
			"    (Ein Bericht mit den erkannten Ports liegt trotzdem in %s.)", lastOpenErr, outPath)
	}

	fmt.Println("fertig.")
	fmt.Printf("\nBericht geschrieben: %s\n", outPath)
	fmt.Println("Bitte diese Datei zurückschicken — sie enthält nur Funkdaten und Geräteangaben,")
	fmt.Println("keine Passwörter und keine Schlüssel.")
	return nil
}

func parseBauds(list string) ([]int, error) {
	var out []int
	for _, part := range strings.Split(list, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		baud, err := strconv.Atoi(part)
		if err != nil || baud <= 0 {
			return nil, fmt.Errorf("ungültige Baudrate %q", part)
		}
		out = append(out, baud)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("keine Baudrate angegeben")
	}
	return out, nil
}

// resolvePort returns the device path to use and records what the system
// reports about every serial port it can see. With no -port given it
// picks the only USB serial port present; anything else is reported as a
// list for the operator to choose from, since guessing between several
// would be worse than asking.
//
// Unlike the collector's own detection this asks for active USB probing
// on every device (enumerator.All), which is what fills in manufacturer
// and product strings — worth it for a diagnostic someone runs by hand
// once, where identifying the device is the whole point.
func resolvePort(portPath string, report *strings.Builder) (string, error) {
	ports, err := enumerator.GetDetailedPortsList(enumerator.All)
	if err != nil {
		fmt.Fprintf(report, "Serielle Ports: konnten nicht aufgelistet werden (%v)\n\n", err)
		if portPath == "" {
			return "", fmt.Errorf("serielle Ports auflisten: %w", err)
		}
		return portPath, nil
	}

	report.WriteString("Serielle Ports:\n")
	var usb []string
	for _, p := range ports {
		fmt.Fprintf(report, "  %s  USB=%v VID=%s PID=%s Seriennummer=%q Hersteller=%q Produkt=%q\n",
			p.Name, p.IsUSB, p.VID, p.PID, p.SerialNumber, p.Manufacturer, p.Product)
		if p.IsUSB {
			usb = append(usb, p.Name)
		}
	}
	report.WriteString("\n")

	if portPath != "" {
		return portPath, nil
	}
	switch len(usb) {
	case 1:
		return usb[0], nil
	case 0:
		return "", fmt.Errorf("kein USB-Seriell-Port gefunden — Empfänger eingesteckt? Sonst mit -port /dev/ttyUSBx angeben")
	default:
		return "", fmt.Errorf("mehrere USB-Seriell-Ports gefunden (%s) — bitte einen mit -port auswählen", strings.Join(usb, ", "))
	}
}

// probeResponse is what the device answered to the config-mode probe.
type probeResponse struct {
	attempted bool
	toEnter   []byte // answer to the single 0x00
	toExit    []byte // answer to 'X'
}

func probeSummary(p probeResponse) string {
	if !p.attempted {
		return ""
	}
	if len(p.toEnter) == 0 && len(p.toExit) == 0 {
		return ", Sonde: keine Antwort"
	}
	return fmt.Sprintf(", Sonde: %d/%d Bytes Antwort", len(p.toEnter), len(p.toExit))
}

func listenAt(path string, baud int, listen time.Duration, probe bool) ([]byte, probeResponse, error) {
	p, err := serial.Open(path, &serial.Mode{
		BaudRate: baud,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
		// Matching what saCollector's own port opener learned about USB
		// bridges: left implicit, Windows and Linux disagree on whether
		// DTR/RTS get asserted, and many bridges treat the transition as
		// a device reset.
		InitialStatusBits: &serial.ModemOutputBits{DTR: true, RTS: true},
	})
	if err != nil {
		return nil, probeResponse{}, describeOpenError(path, err)
	}
	defer p.Close()

	if err := p.ResetInputBuffer(); err != nil {
		return nil, probeResponse{}, err
	}
	if err := p.SetReadTimeout(500 * time.Millisecond); err != nil {
		return nil, probeResponse{}, err
	}

	captured := readFor(p, listen, true)

	var resp probeResponse
	if probe {
		resp.attempted = true
		if _, err := p.Write([]byte{0x00}); err == nil {
			resp.toEnter = readFor(p, 2*time.Second, false)
		}
		if _, err := p.Write([]byte{'X'}); err == nil {
			resp.toExit = readFor(p, time.Second, false)
		}
	}
	return captured, resp, nil
}

// readFor collects everything arriving within d. progress prints a line
// every few seconds during the long passive phase, so someone watching a
// terminal for a minute per baud rate can see it is still alive.
func readFor(p serial.Port, d time.Duration, progress bool) []byte {
	var out []byte
	deadline := time.Now().Add(d)
	nextTick := time.Now().Add(10 * time.Second)
	buf := make([]byte, 4096)
	for time.Now().Before(deadline) {
		n, err := p.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
		}
		if err != nil {
			break
		}
		if progress && time.Now().After(nextTick) {
			fmt.Printf("   … %ds, %d Bytes\n", int(d.Seconds()-time.Until(deadline).Seconds()), len(out))
			nextTick = time.Now().Add(10 * time.Second)
		}
	}
	return out
}

// describeOpenError turns the one failure that reliably trips up a first
// run on Ubuntu — the user not being in the dialout group — into the
// instruction that fixes it, instead of a bare "permission denied" that
// costs a round trip to explain.
func describeOpenError(path string, err error) error {
	if strings.Contains(strings.ToLower(err.Error()), "permission denied") {
		return fmt.Errorf("%s: %w\n\n"+
			"    Unter Ubuntu fehlt dafür meist die Gruppe \"dialout\":\n"+
			"        sudo usermod -a -G dialout $USER\n"+
			"    Danach ab- und wieder anmelden (oder neu starten) und erneut versuchen.\n"+
			"    Zum schnellen Test geht auch: sudo go run ./collector/cmd/receiverdump", path, err)
	}
	return fmt.Errorf("%s: %w", path, err)
}

func writeBaudSection(report *strings.Builder, baud int, captured []byte, probe probeResponse) {
	fmt.Fprintf(report, "== %d Baud ==\n", baud)
	fmt.Fprintf(report, "Empfangen: %d Bytes\n", len(captured))

	if probe.attempted {
		fmt.Fprintf(report, "Sonde 0x00 -> % X   %q\n", probe.toEnter, printable(probe.toEnter))
		fmt.Fprintf(report, "Sonde 'X'  -> % X   %q\n", probe.toExit, printable(probe.toExit))
		if isRadiocraftsPrompt(probe.toEnter) {
			report.WriteString("  ==> sieht nach einem Radiocrafts-Konfigurationsmodus aus ('>'-Prompt)\n")
		}
	}

	if len(captured) > 0 {
		writeAnalysis(report, captured)
		report.WriteString("\nHexdump:\n")
		limit := captured
		if len(limit) > dumpLimit {
			limit = limit[:dumpLimit]
		}
		report.WriteString(hex.Dump(limit))
		if len(captured) > dumpLimit {
			fmt.Fprintf(report, "... (%d weitere Bytes nicht abgedruckt)\n", len(captured)-dumpLimit)
		}
	}
	report.WriteString("\n")
}

// isRadiocraftsPrompt reports whether a config-mode probe was answered
// with the '>' prompt a Radiocrafts module opens its command interface
// with. Checked anywhere in the answer, not just at the start: a module
// mid-transmission can put frame bytes ahead of it.
func isRadiocraftsPrompt(b []byte) bool {
	for _, c := range b {
		if c == '>' {
			return true
		}
	}
	return false
}

// writeAnalysis is the part that can make a second round trip
// unnecessary: rather than only dumping bytes for someone else to stare
// at, it checks the two structures that actually distinguish the
// candidate protocols, and — the decisive one — tries to find a genuine
// wM-Bus telegram in the capture using the collector's own parser.
func writeAnalysis(report *strings.Builder, b []byte) {
	report.WriteString("\nAnalyse:\n")

	slip := 0
	printableCount := 0
	for _, c := range b {
		if c == 0xC0 {
			slip++
		}
		if c >= 0x20 && c < 0x7F {
			printableCount++
		}
	}
	fmt.Fprintf(report, "  0xC0-Bytes (SLIP-Rahmung wie beim IMST): %d\n", slip)
	fmt.Fprintf(report, "  druckbare ASCII-Zeichen: %d von %d\n", printableCount, len(b))

	for _, stride := range []struct {
		extra int
		label string
	}{
		{0, "L+1 (nur Längenbyte, kein RSSI)"},
		{1, "L+2 (Längenbyte + ein angehängtes Byte, z. B. RSSI)"},
	} {
		if n, ok := chainLength(b, stride.extra); ok {
			fmt.Fprintf(report, "  längenpräfixierte Kette ab Offset 0, Schrittweite %s: %d Rahmen\n", stride.label, n)
		}
	}

	if found := findTelegram(report, b); !found {
		report.WriteString("  kein gültiges wM-Bus-Telegramm gefunden\n")
	}
}

// chainLength walks the capture as if every frame were a length byte
// followed by that many bytes (plus extra trailing bytes, e.g. an RSSI
// byte), and reports how many frames that assumption survives. A wrong
// assumption runs into the end of the buffer or an impossible length
// almost immediately, so a chain of several frames is a strong hint.
func chainLength(b []byte, extra int) (int, bool) {
	frames, offset := 0, 0
	for offset < len(b) {
		l := int(b[offset])
		if l < 10 { // shorter than a wM-Bus header can possibly be
			return frames, frames >= 2
		}
		next := offset + 1 + l + extra
		if next > len(b) {
			return frames, frames >= 2
		}
		frames++
		offset = next
	}
	return frames, frames >= 2
}

// findTelegram scans for a byte sequence the collector's own parser
// accepts as a wM-Bus telegram, and reports where it sits and which
// meter it came from. Anchored on the C field (0x44, SND-NR — what a
// meter's periodic broadcast uses) with the length byte in front of it,
// which is cheap to test at every offset and specific enough not to fire
// on noise, since ParseWMBus then still has to agree.
func findTelegram(report *strings.Builder, b []byte) bool {
	found := 0
	for i := 1; i+1 < len(b) && found < maxTelegramExamples; i++ {
		if b[i] != 0x44 {
			continue
		}
		start := i - 1
		l := int(b[start])
		end := start + 1 + l
		if l < 10 || end > len(b) {
			continue
		}
		f, err := telegram.ParseWMBus(b[start:end])
		if err != nil {
			continue
		}
		meter, err := f.MeterNumber()
		if err != nil {
			continue
		}
		fmt.Fprintf(report, "  ==> gültiges wM-Bus-Telegramm bei Offset %d, Länge %d, Zähler %s\n", start, l+1, meter)
		fmt.Fprintf(report, "      danach folgt: % X\n", b[end:min(end+4, len(b))])
		fmt.Fprintf(report, "      roh: %s\n", hex.EncodeToString(b[start:end]))
		found++
	}
	return found > 0
}

// maxTelegramExamples bounds how many finds go into the report: the
// question it answers ("what does a frame look like, and what surrounds
// it") is settled by a handful, and a busy building would otherwise fill
// the file with hundreds of identical-looking entries.
const maxTelegramExamples = 5

func printable(b []byte) string {
	var out strings.Builder
	for _, c := range b {
		if c >= 0x20 && c < 0x7F {
			out.WriteByte(c)
		} else {
			out.WriteByte('.')
		}
	}
	return out.String()
}
