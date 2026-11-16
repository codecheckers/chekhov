package check

import "testing"

// A family name is found in a name however the accents and umlauts are
// written on either side, and a different name is not (CC-MET-003).
func TestContainsNameFoldsAccentsAndUmlauts(t *testing.T) {
	for _, c := range []struct {
		name, family string
		want         bool
	}{
		{"Daniel Nüst", "Nüst", true},
		{"Daniel Nuest", "Nüst", true},
		{"Daniel Nust", "Nüst", true},
		{"Daniel Nüst", "Nuest", true},
		{"DANIEL NÜST", "Nüst", true},
		{"Néstor García", "Garcia", true},
		{"Nestor Garcia", "García", true},
		{"Daniel Nüst", "Lovelace", false},
	} {
		if got := containsName(c.name, c.family); got != c.want {
			t.Errorf("containsName(%q, %q) = %v, want %v", c.name, c.family, got, c.want)
		}
	}
}
