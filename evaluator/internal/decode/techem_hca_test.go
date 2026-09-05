package decode

import "testing"

func TestPatchTechemHCARoundTrips(t *testing.T) {
	raw := make([]byte, techemHCAFrameLen)
	raw[18], raw[19] = 0x00, 0x00 // current reading starts at 0

	patched, err := PatchManufacturerSpecific("TCH", raw, 535)
	if err != nil {
		t.Fatalf("PatchManufacturerSpecific: %v", err)
	}
	if !patched {
		t.Fatal("expected a patcher to be registered for TCH")
	}

	reading, ok, err := decodeTechemHCA(raw)
	if err != nil || !ok {
		t.Fatalf("decodeTechemHCA after patch: ok=%v err=%v", ok, err)
	}
	if reading.Value != 535 {
		t.Errorf("reading.Value = %d, want 535", reading.Value)
	}
}

func TestPatchTechemHCALeavesOtherBytesUntouched(t *testing.T) {
	raw := make([]byte, techemHCAFrameLen)
	for i := range raw {
		raw[i] = byte(i)
	}
	before := append([]byte(nil), raw...)

	if _, err := PatchManufacturerSpecific("TCH", raw, 999); err != nil {
		t.Fatalf("PatchManufacturerSpecific: %v", err)
	}
	for i := range raw {
		if i >= 18 && i < 20 {
			continue // the patched field itself
		}
		if raw[i] != before[i] {
			t.Errorf("byte %d changed from 0x%02X to 0x%02X, want untouched", i, before[i], raw[i])
		}
	}
}

func TestPatchTechemHCARejectsWrongLength(t *testing.T) {
	if _, err := PatchManufacturerSpecific("TCH", make([]byte, 10), 100); err == nil {
		t.Error("expected an error for a frame of the wrong length")
	}
}

func TestPatchTechemHCARejectsOutOfRange(t *testing.T) {
	raw := make([]byte, techemHCAFrameLen)
	if _, err := PatchManufacturerSpecific("TCH", raw, -1); err == nil {
		t.Error("expected an error for a negative value")
	}
	if _, err := PatchManufacturerSpecific("TCH", raw, 0x10000); err == nil {
		t.Error("expected an error for a value that does not fit in 16 bits")
	}
}

func TestPatchManufacturerSpecificUnknownManufacturer(t *testing.T) {
	patched, err := PatchManufacturerSpecific("ZZZ", nil, 1)
	if err != nil || patched {
		t.Errorf("PatchManufacturerSpecific(unknown) = patched=%v err=%v, want false/nil", patched, err)
	}
}
