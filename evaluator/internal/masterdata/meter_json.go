package masterdata

import (
	"encoding/hex"
	"encoding/json"
	"fmt"

	"selbst-ableser/internal/telegram"
)

// meterJSON is Meter's on-disk/export shape: the AES key as a hex string
// rather than a raw byte array, so a saved or exported file stays readable
// text (STAMM-07 requires an open, documented export format) instead of a
// wall of small numbers.
type meterJSON struct {
	Number       string        `json:"number"`
	MeterPointID string        `json:"meter_point_id"`
	AESKey       string        `json:"aes_key"` // 32 hex characters
	InstalledAt  telegram.Day  `json:"installed_at"`
	StartReading int64         `json:"start_reading"`
	RemovedAt    *telegram.Day `json:"removed_at,omitempty"`
	EndReading   *int64        `json:"end_reading,omitempty"`
	KCFactor     float64       `json:"kc_factor,omitempty"`
	ResetMonth   int           `json:"reset_month,omitempty"`
}

func (m Meter) MarshalJSON() ([]byte, error) {
	return json.Marshal(meterJSON{
		Number:       m.Number,
		MeterPointID: m.MeterPointID,
		AESKey:       hex.EncodeToString(m.AESKey[:]),
		InstalledAt:  m.InstalledAt,
		StartReading: m.StartReading,
		RemovedAt:    m.RemovedAt,
		EndReading:   m.EndReading,
		KCFactor:     m.KCFactor,
		ResetMonth:   m.ResetMonth,
	})
}

func (m *Meter) UnmarshalJSON(data []byte) error {
	var mj meterJSON
	if err := json.Unmarshal(data, &mj); err != nil {
		return err
	}
	key, err := hex.DecodeString(mj.AESKey)
	if err != nil {
		return fmt.Errorf("masterdata: meter %s: AES key is not valid hex: %w", mj.Number, err)
	}
	if len(key) != 16 {
		return fmt.Errorf("masterdata: meter %s: AES key must be 16 bytes (32 hex characters), got %d", mj.Number, len(key))
	}

	m.Number = mj.Number
	m.MeterPointID = mj.MeterPointID
	copy(m.AESKey[:], key)
	m.InstalledAt = mj.InstalledAt
	m.StartReading = mj.StartReading
	m.RemovedAt = mj.RemovedAt
	m.EndReading = mj.EndReading
	m.KCFactor = mj.KCFactor
	m.ResetMonth = mj.ResetMonth
	return nil
}
