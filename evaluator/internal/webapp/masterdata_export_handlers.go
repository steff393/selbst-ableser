package webapp

import (
	"encoding/json"
	"net/http"

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
		export.Meters = append(export.Meters, meterRowFromMeter(m))
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
	if err := json.NewEncoder(w).Encode(export); err != nil {
		a.logger().Error("streaming masterdata export", "err", err)
	}
}
