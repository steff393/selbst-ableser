package masterdata

import (
	"encoding/json"
	"strings"
	"testing"
)

const testMeterJSONPrefix = `{"number":"10004711","meter_point_id":"101","aes_key":"000102030405060708090a0b0c0d0e0f","installed_at":"2024-01-01","start_reading":0`

func TestMeterJSONWritesFactor(t *testing.T) {
	var m Meter
	if err := json.Unmarshal([]byte(testMeterJSONPrefix+`}`), &m); err != nil {
		t.Fatal(err)
	}
	m.Factor = 1.2
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"factor":1.2`) || strings.Contains(string(out), "kc_factor") {
		t.Errorf("marshalled = %s, want factor and no kc_factor", out)
	}
}

// Master data and exports saved before the rename call the field
// kc_factor; they must still load with their factors.
func TestMeterJSONReadsLegacyKCFactor(t *testing.T) {
	cases := []struct {
		name string
		tail string
		want float64
	}{
		{"new name", `,"factor":1.4}`, 1.4},
		{"legacy name", `,"kc_factor":1.3}`, 1.3},
		{"both, new wins", `,"factor":1.4,"kc_factor":1.3}`, 1.4},
		{"neither", `}`, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var m Meter
			if err := json.Unmarshal([]byte(testMeterJSONPrefix+c.tail), &m); err != nil {
				t.Fatal(err)
			}
			if m.Factor != c.want {
				t.Errorf("Factor = %v, want %v", m.Factor, c.want)
			}
		})
	}
}
