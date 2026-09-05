//go:build windows

package receiver

import "testing"

func withFakePnPQuery(t *testing.T, output string) {
	t.Helper()
	original := runPowerShellPnPQuery
	runPowerShellPnPQuery = func() ([]byte, error) { return []byte(output), nil }
	t.Cleanup(func() { runPowerShellPnPQuery = original })
}

func TestFindReceiverPortsViaWMIParsesCompositeDeviceID(t *testing.T) {
	// Real output shape from a machine with an IMST iU891A-XL attached:
	// its serial interface enumerates under "PORTS\", not "USB\", which
	// is exactly the case go.bug.st/serial/enumerator's own VID/PID
	// parser misses (see this file's package doc comment).
	withFakePnPQuery(t, `[{"Name":"USB Serial Port (COM14)","DeviceID":"PORTS\\VID_04B4&PID_0003&MI_00\\IMS3015"}]`)

	got, err := findReceiverPortsViaWMI()
	if err != nil {
		t.Fatalf("findReceiverPortsViaWMI: %v", err)
	}
	if len(got) != 1 || got[0] != "COM14" {
		t.Errorf("findReceiverPortsViaWMI = %v, want [COM14]", got)
	}
}

func TestFindReceiverPortsViaWMIIgnoresNonMatchingDevices(t *testing.T) {
	withFakePnPQuery(t, `[{"Name":"Some Other Device (COM5)","DeviceID":"PORTS\\VID_1234&PID_5678\\XYZ"}]`)

	got, err := findReceiverPortsViaWMI()
	if err != nil {
		t.Fatalf("findReceiverPortsViaWMI: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("findReceiverPortsViaWMI = %v, want no matches for an unrelated VID/PID", got)
	}
}

func TestFindReceiverPortsViaWMIHandlesEmptyResult(t *testing.T) {
	withFakePnPQuery(t, `[]`)

	got, err := findReceiverPortsViaWMI()
	if err != nil {
		t.Fatalf("findReceiverPortsViaWMI: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("findReceiverPortsViaWMI = %v, want none", got)
	}
}
