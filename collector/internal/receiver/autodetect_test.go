package receiver

import (
	"testing"

	"go.bug.st/serial/enumerator"
)

func withFakePorts(t *testing.T, ports []*enumerator.PortDetails) {
	t.Helper()
	original := listPorts
	listPorts = func(filters ...func(vid, pid string) bool) ([]*enumerator.PortDetails, error) {
		return ports, nil
	}
	t.Cleanup(func() { listPorts = original })

	// Tests must be hermetic even on a machine that happens to have a
	// real matching receiver plugged in — see autodetect_windows.go's
	// fallback, which would otherwise make TestFindReceiverPortNoMatch
	// and TestFindReceiverPortIgnoresNonUSB depend on actual hardware.
	originalFallback := platformFallbackPorts
	platformFallbackPorts = nil
	t.Cleanup(func() { platformFallbackPorts = originalFallback })
}

func TestFindReceiverPortNoMatch(t *testing.T) {
	withFakePorts(t, []*enumerator.PortDetails{
		{Name: "COM3", IsUSB: true, VID: "1234", PID: "5678"},
	})
	_, found, err := FindReceiverPort()
	if err != nil {
		t.Fatalf("FindReceiverPort: %v", err)
	}
	if found {
		t.Error("expected no match for an unrelated VID/PID")
	}
}

func TestFindReceiverPortSingleMatch(t *testing.T) {
	withFakePorts(t, []*enumerator.PortDetails{
		{Name: "COM3", IsUSB: true, VID: "1234", PID: "5678"},
		{Name: "COM14", IsUSB: true, VID: "04b4", PID: "0003"}, // lowercase, as some platforms report it
	})
	port, found, err := FindReceiverPort()
	if err != nil {
		t.Fatalf("FindReceiverPort: %v", err)
	}
	if !found || port != "COM14" {
		t.Errorf("FindReceiverPort = %q, %v, want COM14, true", port, found)
	}
}

func TestFindReceiverPortIgnoresNonUSB(t *testing.T) {
	withFakePorts(t, []*enumerator.PortDetails{
		{Name: "COM1", IsUSB: false, VID: imstVendorID, PID: imstProductID},
	})
	_, found, err := FindReceiverPort()
	if err != nil {
		t.Fatalf("FindReceiverPort: %v", err)
	}
	if found {
		t.Error("a non-USB port must never match, even with a coincidentally matching VID/PID")
	}
}

func TestFindReceiverPortMultipleMatchesRefusesToGuess(t *testing.T) {
	withFakePorts(t, []*enumerator.PortDetails{
		{Name: "COM14", IsUSB: true, VID: imstVendorID, PID: imstProductID},
		{Name: "COM15", IsUSB: true, VID: imstVendorID, PID: imstProductID},
	})
	_, found, err := FindReceiverPort()
	if err == nil {
		t.Fatal("expected an error when multiple receivers match, not a silent pick")
	}
	if found {
		t.Error("found should be false when the result is an error")
	}
}
