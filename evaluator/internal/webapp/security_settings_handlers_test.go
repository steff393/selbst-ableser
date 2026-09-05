package webapp

import "testing"

func TestHostListAllows(t *testing.T) {
	cases := []struct {
		name    string
		allowed []string
		host    string
		want    bool
	}{
		{"empty list allows anything", nil, "whatever.example", true},
		{"exact match", []string{"app.example"}, "app.example", true},
		{"port is ignored", []string{"app.example"}, "app.example:8226", true},
		{"unlisted host", []string{"app.example"}, "other.example", false},
		{"second entry matches", []string{"a.example", "b.example"}, "b.example", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hostListAllows(c.allowed, c.host); got != c.want {
				t.Errorf("hostListAllows(%v, %q) = %v, want %v", c.allowed, c.host, got, c.want)
			}
		})
	}
}

// TestSplitNonEmpty: an empty field must mean "unrestricted" (nil), never
// a one-element list containing "", which would reject every request
// including the operator's own.
func TestSplitNonEmpty(t *testing.T) {
	if got := splitNonEmpty("", ","); got != nil {
		t.Errorf("splitNonEmpty(\"\") = %v, want nil", got)
	}
	if got := splitNonEmpty("  ,  ", ","); got != nil {
		t.Errorf("splitNonEmpty of only separators = %v, want nil", got)
	}
	got := splitNonEmpty(" a.example , b.example ", ",")
	if len(got) != 2 || got[0] != "a.example" || got[1] != "b.example" {
		t.Errorf("splitNonEmpty = %v, want [a.example b.example]", got)
	}
}
