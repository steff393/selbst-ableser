package billing

import "selbst-ableser/internal/telegram"

// ClipToAccess implements FACH-10's server-side range restriction: start
// becomes the later of the requested start and the access period's start
// (move-in), end becomes the earlier of the requested end and the access
// period's end (move-out, if any). The result never depends on the
// requested bounds alone. An empty intersection is reported rather than
// an invalid (start after end) range.
func ClipToAccess(requestedStart, requestedEnd, accessStart telegram.Day, accessEnd *telegram.Day) (start, end telegram.Day, empty bool) {
	start = maxDay(requestedStart, accessStart)
	end = requestedEnd
	if accessEnd != nil {
		end = minDay(end, *accessEnd)
	}
	if end.Before(start) {
		return "", "", true
	}
	return start, end, false
}

func maxDay(a, b telegram.Day) telegram.Day {
	if a.Before(b) {
		return b
	}
	return a
}

func minDay(a, b telegram.Day) telegram.Day {
	if a.Before(b) {
		return a
	}
	return b
}
