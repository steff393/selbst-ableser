package telegram

import (
	_ "embed"
	"encoding/json"
)

//go:embed manufacturers.json
var manufacturersJSON []byte

// manufacturerNames maps a three-letter manufacturer code (see Manufacturer)
// to its registered full name, from the public OMS/DLMS manufacturer ID
// registry.
var manufacturerNames map[string]string

func init() {
	if err := json.Unmarshal(manufacturersJSON, &manufacturerNames); err != nil {
		panic("telegram: invalid embedded manufacturers.json: " + err.Error())
	}
}

// ManufacturerName looks up the full registered name for a three-letter
// manufacturer code. ok is false for a code not in the registry (a device
// from a manufacturer too new or too obscure to be listed, or a corrupted
// M-field).
func ManufacturerName(code string) (string, bool) {
	name, ok := manufacturerNames[code]
	return name, ok
}
