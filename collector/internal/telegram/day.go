package telegram

import (
	"fmt"
	"regexp"
	"time"
)

// Local is the time zone all timestamps and calendar dates are stored and
// processed in. The system is operated exclusively in Germany, and storing
// everything in local time avoids a UTC/local conversion step at every
// boundary.
var Local = mustLoadLocation("Europe/Berlin")

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(fmt.Sprintf("telegram: cannot load time zone %q: %v", name, err))
	}
	return loc
}

// Day is a calendar date (no time-of-day, no time zone), used for billing
// periods and for the one-entry-per-meter-per-day archive key. Representing
// it as a plain string keeps it unambiguous, unlike a time.Time truncated to
// midnight, which is affected by daylight-saving transitions.
type Day string

var dayPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// ParseDay validates and returns a Day from its "YYYY-MM-DD" form.
func ParseDay(s string) (Day, error) {
	if !dayPattern.MatchString(s) {
		return "", fmt.Errorf("telegram: invalid day %q, want YYYY-MM-DD", s)
	}
	if _, err := time.ParseInLocation("2006-01-02", s, Local); err != nil {
		return "", fmt.Errorf("telegram: invalid day %q: %w", s, err)
	}
	return Day(s), nil
}

// DayOf returns the calendar Day of t, interpreted in Local.
func DayOf(t time.Time) Day {
	return Day(t.In(Local).Format("2006-01-02"))
}

func (d Day) String() string { return string(d) }

// Before reports whether d is a calendar date strictly before o.
func (d Day) Before(o Day) bool { return string(d) < string(o) }

// AddDays returns the calendar date n days after d (n may be negative).
// d must already be a valid Day (see ParseDay); passing an invalid one
// panics, since every Day in this codebase is expected to have been
// validated at the point it was constructed.
func (d Day) AddDays(n int) Day {
	t, err := time.ParseInLocation("2006-01-02", string(d), Local)
	if err != nil {
		panic(fmt.Sprintf("telegram: Day.AddDays called on invalid Day %q: %v", d, err))
	}
	return DayOf(t.AddDate(0, 0, n))
}

// Month returns d's calendar month, 1-12.
func (d Day) Month() int {
	t, err := time.ParseInLocation("2006-01-02", string(d), Local)
	if err != nil {
		panic(fmt.Sprintf("telegram: Day.Month called on invalid Day %q: %v", d, err))
	}
	return int(t.Month())
}

// DaysUntil returns the number of whole calendar days from d to other
// (negative if other is before d).
func (d Day) DaysUntil(other Day) int {
	from, err1 := time.ParseInLocation("2006-01-02", string(d), Local)
	to, err2 := time.ParseInLocation("2006-01-02", string(other), Local)
	if err1 != nil || err2 != nil {
		panic(fmt.Sprintf("telegram: Day.DaysUntil called on an invalid Day (%q, %q)", d, other))
	}
	return int(to.Sub(from).Hours() / 24)
}
