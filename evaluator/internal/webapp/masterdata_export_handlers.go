package webapp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"selbst-ableser/internal/access"
	"selbst-ableser/internal/masterdata"
)

// exportUnit/exportMeterPoint/exportAccess mirror their masterdata
// counterparts with the Kind enum spelled out and JSON field names fixed,
// so the export is self-describing without needing this source tree to
// interpret it (STAMM-07: "ein offenes, dokumentiertes Format").
type exportUnit struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	AreaM2 float64 `json:"area_m2"`
}

type exportMeterPoint struct {
	ID     string `json:"id"`
	UnitID string `json:"unit_id"`
	Room   string `json:"room"`
	Kind   string `json:"kind"`
}

type exportAccess struct {
	Token  string `json:"token"`
	UnitID string `json:"unit_id"`
	Start  string `json:"start"`
	End    string `json:"end,omitempty"`
	Email  string `json:"email,omitempty"`
}

type masterDataExport struct {
	ExportedAt  string              `json:"exported_at"`
	Building    masterdata.Building `json:"building"`
	Units       []exportUnit        `json:"units"`
	MeterPoints []exportMeterPoint  `json:"meter_points"`
	Meters      []meterRow          `json:"meters"` // meterRow already hex-encodes the AES key (masterdata_handlers.go)
	Accesses    []exportAccess      `json:"accesses"`
}

// handleMasterDataExport is STAMM-07's first export: the complete master
// data, including AES keys, in an open JSON format independent of this
// program — data portability, and simultaneously a readable backup of
// the master data (a warning belongs on the page that links here, not in
// the handler: STAMM-07 only requires "eine deutliche Warnung beim
// Export", not additional encryption).
func (a *App) handleMasterDataExport(w http.ResponseWriter, r *http.Request) {
	sess := a.session(r)
	if err := access.RequireOperator(sess); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	md, ok := a.Vault.Get()
	if !ok {
		a.renderLocked(w, sess)
		return
	}

	export := masterDataExport{ExportedAt: a.now().Format("2006-01-02T15:04:05")}
	export.Building = md.Building
	for _, u := range md.Units {
		export.Units = append(export.Units, exportUnit{ID: u.ID, Name: u.Name, AreaM2: u.AreaM2})
	}
	for _, mp := range md.MeterPoints {
		export.MeterPoints = append(export.MeterPoints, exportMeterPoint{
			ID: mp.ID, UnitID: mp.UnitID, Room: mp.Room, Kind: kindToString(mp.Kind),
		})
	}
	for _, m := range md.Meters {
		export.Meters = append(export.Meters, meterRowFromMeter(m, kindOfPlace(md.MeterPoints, m.MeterPointID)))
	}
	for _, ac := range md.Accesses {
		ea := exportAccess{Token: ac.Token, UnitID: ac.UnitID, Start: string(ac.Start), Email: ac.Email}
		if ac.End != nil {
			ea.End = string(*ac.End)
		}
		export.Accesses = append(export.Accesses, ea)
	}

	a.audit(access.EventDataIngested, sess.AuditActor(), "masterdata export")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=stammdaten-export.json")
	if err := writeMasterDataExport(w, export); err != nil {
		a.logger().Error("streaming masterdata export", "err", err)
	}
}

// writeMasterDataExport writes export by hand rather than through a
// single json.Marshal: an installation with forty meters produced one
// unreadable line, everything on it. Each Wohnung, Zählerplatz and Zähler
// gets its own compact line instead — indenting every single field would
// spread the same forty meters over two hundred lines and be no easier to
// scan.
func writeMasterDataExport(w http.ResponseWriter, export masterDataExport) error {
	var b strings.Builder
	b.WriteString("{\n")

	exportedAt, err := json.Marshal(export.ExportedAt)
	if err != nil {
		return err
	}
	fmt.Fprintf(&b, "  \"exported_at\": %s,\n", exportedAt)

	building, err := json.Marshal(export.Building)
	if err != nil {
		return err
	}
	fmt.Fprintf(&b, "  \"building\": %s,\n", building)

	if err := writeCompactArray(&b, "units", export.Units, true); err != nil {
		return err
	}
	if err := writeCompactArray(&b, "meter_points", export.MeterPoints, true); err != nil {
		return err
	}
	if err := writeCompactArray(&b, "meters", export.Meters, true); err != nil {
		return err
	}
	if err := writeCompactArray(&b, "accesses", export.Accesses, false); err != nil {
		return err
	}
	b.WriteString("}\n")

	_, err = w.Write([]byte(b.String()))
	return err
}

// writeCompactArray writes a JSON array under key, one compactly
// marshaled element per line.
func writeCompactArray[T any](b *strings.Builder, key string, items []T, trailingComma bool) error {
	fmt.Fprintf(b, "  %q: [", key)
	if len(items) == 0 {
		b.WriteString("]")
	} else {
		b.WriteString("\n")
		for i, item := range items {
			raw, err := json.Marshal(item)
			if err != nil {
				return err
			}
			b.WriteString("    ")
			b.Write(raw)
			if i < len(items)-1 {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
		b.WriteString("  ]")
	}
	if trailingComma {
		b.WriteString(",")
	}
	b.WriteString("\n")
	return nil
}
