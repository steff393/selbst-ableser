package receiver

import (
	"fmt"
	"strings"

	"go.bug.st/serial/enumerator"
)

// USB vendor/product ID of the Cypress Semiconductor chip the IMST
// iU891A-XL uses for its serial-over-USB connection. Matching by ID
// rather than by manufacturer/product description string works
// consistently across Windows and Linux — those descriptive strings are
// only populated with "active USB probing" enabled (see
// enumerator.GetDetailedPortsList), which this deliberately keeps
// limited to devices already matching this ID, not every USB device on
// the machine.
const (
	imstVendorID  = "04B4"
	imstProductID = "0003"
)

// listPorts is enumerator.GetDetailedPortsList, indirected so tests can
// supply a fake port list without real hardware or OS-level USB access.
var listPorts = enumerator.GetDetailedPortsList

// platformFallbackPorts finds matching receivers through an OS-specific
// path when the portable enumerator above comes up empty — currently set
// only on Windows (see autodetect_windows.go) to work around a real gap
// in go.bug.st/serial/enumerator's VID/PID detection for composite USB
// devices. nil (the default) means there is no such fallback here.
var platformFallbackPorts func() ([]string, error)

func isIMSTReceiver(vid, pid string) bool {
	return strings.EqualFold(vid, imstVendorID) && strings.EqualFold(pid, imstProductID)
}

// FindReceiverPort looks for a connected IMST iU891A-XL (or USB VID/PID
// compatible) receiver among the system's serial ports. found is false,
// not an error, when nothing currently matches — the receiver might
// simply not be plugged in yet, an expected, retryable condition, not a
// fatal one. Finding more than one is reported as an error rather than
// picking one at random, the same "refuse to guess" rule the USB-stick
// backup detection already follows: an operator with several identical
// receivers on one machine must pick one explicitly via -port.
func FindReceiverPort() (port string, found bool, err error) {
	ports, err := listPorts(isIMSTReceiver)
	if err != nil {
		return "", false, fmt.Errorf("receiver: listing serial ports: %w", err)
	}

	var matches []string
	for _, p := range ports {
		if p.IsUSB && isIMSTReceiver(p.VID, p.PID) {
			matches = append(matches, p.Name)
		}
	}

	if len(matches) == 0 && platformFallbackPorts != nil {
		fallback, err := platformFallbackPorts()
		if err != nil {
			return "", false, err
		}
		matches = fallback
	}

	switch len(matches) {
	case 0:
		return "", false, nil
	case 1:
		return matches[0], true, nil
	default:
		return "", false, fmt.Errorf("receiver: multiple IMST receivers found (%s); specify -port to pick one", strings.Join(matches, ", "))
	}
}

// OpenAutoDetectedPort returns a PortOpener that runs FindReceiverPort on
// every (re)connect attempt, rather than a fixed path found once at
// startup — so the same reconnect-with-backoff loop SerialSource already
// has for a lost connection also covers a receiver that reappears on a
// different port after being unplugged and replugged (common on
// Windows), with no special-casing needed here.
func OpenAutoDetectedPort() PortOpener {
	return func() (Port, string, error) {
		path, found, err := FindReceiverPort()
		if err != nil {
			return nil, "", err
		}
		if !found {
			return nil, "", fmt.Errorf("receiver: not connected")
		}
		return OpenSerialPort(path)()
	}
}
