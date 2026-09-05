//go:build windows

package receiver

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
)

// go.bug.st/serial/enumerator only recognizes a USB serial device's VID/
// PID when its Windows device instance ID starts with "USB\" or
// "FTDIBUS\" (see its parseDeviceID). Composite USB devices — the IMST
// iU891A-XL among them — instead enumerate their serial interface under
// the "PORTS\" device class (e.g. "PORTS\VID_04B4&PID_0003&MI_00\..."),
// which that parser silently ignores, leaving VID/PID empty even though
// Windows itself has both. This queries the same underlying data through
// CIM/WMI, which is not limited that way, as a fallback for exactly that
// case — tried only when the primary enumerator-based lookup found
// nothing, not as a replacement for it.
var runPowerShellPnPQuery = func() ([]byte, error) {
	// -InputObject (not a pipeline) is what actually keeps ConvertTo-Json
	// from collapsing a single match down to a bare object instead of a
	// one-element array — piping an already-@()-wrapped array through it
	// still gets unwrapped by the pipeline itself before ConvertTo-Json
	// ever sees it.
	const script = `ConvertTo-Json -Compress -InputObject @(Get-CimInstance -ClassName Win32_PnPEntity | Where-Object { $_.Name -match '\(COM\d+\)' } | Select-Object Name, DeviceID)`
	return exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).Output()
}

var (
	comPortNamePattern = regexp.MustCompile(`\(COM\d+\)`)
	vidPidPattern      = regexp.MustCompile(`(?i)VID_([0-9A-F]{4})&PID_([0-9A-F]{4})`)
)

func init() {
	platformFallbackPorts = findReceiverPortsViaWMI
}

type pnpEntity struct {
	Name     string `json:"Name"`
	DeviceID string `json:"DeviceID"`
}

// findReceiverPortsViaWMI returns the port names of every currently
// present device matching the IMST vendor/product ID, found through
// Windows' CIM/WMI device list rather than go.bug.st/serial/enumerator.
func findReceiverPortsViaWMI() ([]string, error) {
	out, err := runPowerShellPnPQuery()
	if err != nil {
		return nil, fmt.Errorf("receiver: querying Windows device list: %w", err)
	}

	var entities []pnpEntity
	if err := json.Unmarshal(out, &entities); err != nil {
		return nil, fmt.Errorf("receiver: parsing Windows device list: %w", err)
	}

	var matches []string
	for _, e := range entities {
		m := vidPidPattern.FindStringSubmatch(e.DeviceID)
		if m == nil || !isIMSTReceiver(m[1], m[2]) {
			continue
		}
		portMatch := comPortNamePattern.FindString(e.Name)
		if portMatch == "" {
			continue
		}
		matches = append(matches, portMatch[1:len(portMatch)-1]) // strip the parentheses
	}
	return matches, nil
}
