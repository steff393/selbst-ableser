package webapp

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"selbst-ableser/internal/masterdata"
)

// Zählerstände werden für Wasserzähler als m³ mit Komma eingegeben und
// angezeigt — das ist die einzige Einheit, die sonst irgendwo im Projekt
// für Wasser auftaucht (UVI, Zählerstände-Tabelle, Diagramm). Intern
// bleibt der rohe Gerätewert weiterhin in Litern gespeichert (siehe
// docs/04-fachlogik.md, "Warum intern Liter?"); die Umrechnung passiert
// ausschließlich an den Rändern, wo ein Mensch tippt oder liest.
//
// Ein Heizkostenverteiler hat keine Nachkommastellen und kein
// Tausendertrennzeichen — sein roher Wert ist die Einheiten-Anzeige des
// Geräts, unverändert. Ein Wärmemengenzähler zählt ganze kWh und folgt
// derselben strengen Ganzzahl-Regel. Für Gas gibt es noch keinen Decoder
// (O-1) und damit keine etablierte Konvention; es fällt auf dieselbe
// Regel zurück, was zumindest verhindert, dass ein Komma dort unbemerkt
// als Tausendertrennzeichen gelesen wird.

// waterReadingKind reports whether kind's raw value is entered and shown
// as m³ rather than as a plain integer.
func waterReadingKind(kind masterdata.Kind) bool {
	return kind == masterdata.KindHotWater || kind == masterdata.KindColdWater
}

// kindOfPlace resolves a meter point's consumption kind by ID, for the
// many call sites that only have the ID at hand (a form field, a CSV
// column, a grid row) and need to know which convention a reading of
// theirs follows. An unknown or empty ID falls back to the strict,
// integer-only convention — the safe default when the kind cannot be
// established.
func kindOfPlace(points []masterdata.MeterPoint, placeID string) masterdata.Kind {
	for _, mp := range points {
		if mp.ID == placeID {
			return mp.Kind
		}
	}
	return masterdata.KindHeatCostAllocator
}

// formatReadingValue is parseReadingValue's inverse: the raw device value
// (liters for water, the device's own units otherwise) as an operator
// should see it.
func formatReadingValue(kind masterdata.Kind, raw int64) string {
	if waterReadingKind(kind) {
		return formatCubicMeters(raw)
	}
	return strconv.FormatInt(raw, 10)
}

func formatCubicMeters(liters int64) string {
	return strings.Replace(strconv.FormatFloat(float64(liters)/1000, 'f', 3, 64), ".", ",", 1)
}

// parseReadingValue reads an operator-entered meter reading for kind and
// returns the raw device value the archive and internal/decode use it in.
// label names the field in the returned message ("Anfangsstand",
// "Endstand", …); an empty s returns (0, "") — callers that require a
// value check for that themselves, the same way an empty cell already
// meant "not given" everywhere in this codebase.
//
// A comma here is always the decimal mark for a water reading, never a
// column separator — this only ever parses one already-isolated cell, so
// there is no separator to confuse it with.
func parseReadingValue(kind masterdata.Kind, label, s string) (int64, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, ""
	}
	if waterReadingKind(kind) {
		normalized := strings.Replace(s, ",", ".", 1)
		f, err := strconv.ParseFloat(normalized, 64)
		if err != nil || f < 0 {
			return 0, fmt.Sprintf("%s %q ist keine gültige Zahl — m³ mit Komma erwartet, zum Beispiel \"12,345\".", label, s)
		}
		liters := math.Round(f * 1000)
		if math.Abs(f*1000-liters) > 0.05 {
			return 0, fmt.Sprintf("%s %q hat mehr als drei Nachkommastellen — der Zähler löst nur bis auf den Liter auf (0,001 m³).", label, s)
		}
		return int64(liters), ""
	}
	if strings.ContainsAny(s, ",.") {
		return 0, fmt.Sprintf("%s %q darf kein Komma oder einen Punkt enthalten — dieser Zähler zählt in ganzen Einheiten ohne Tausendertrennzeichen.", label, s)
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < 0 {
		return 0, fmt.Sprintf("%s %q ist keine gültige Zahl.", label, s)
	}
	return v, ""
}
