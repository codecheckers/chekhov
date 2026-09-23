package check

import (
	"slices"
	"testing"
)

// The titles are the register's own, as they read when this was written.
func TestTitleCertificates(t *testing.T) {
	cases := []struct {
		title string
		want  []string
	}{
		{"Dewi, Holtrop, DeVito et al | 2026-026", []string{"2026-026"}},
		{"Ruining Dong, Daniel Cameron, Justin Bedo, Anthony T Papenfuss", nil},
		{"AGILEGIS Conference 2026 Reproducibility Reviews | 2026-004/2026-017",
			[]string{"2026-004", "2026-005", "2026-006", "2026-007", "2026-008", "2026-009", "2026-010",
				"2026-011", "2026-012", "2026-013", "2026-014", "2026-015", "2026-016", "2026-017"}},
		{"AGILE Reproducibility Reviews 2025 (2025-008 - 2025-017)",
			[]string{"2025-008", "2025-009", "2025-010", "2025-011", "2025-012", "2025-013",
				"2025-014", "2025-015", "2025-016", "2025-017"}},
		{"Zauner, Udovicic, Spitschan - 2024", nil},
		{"A range across the year | 2025-030/2026-002", []string{"2025-030", "2026-002"}},
		{"A range backwards | 2026-009/2026-003", []string{"2026-009", "2026-003"}},
		{"Twice | 2026-001, again 2026-001", []string{"2026-001"}},
		{"Not an identifier | 12026-0011", nil},
	}
	for _, testCase := range cases {
		if got := TitleCertificates(testCase.title); !slices.Equal(got, testCase.want) {
			t.Errorf("%q: got %v, want %v", testCase.title, got, testCase.want)
		}
	}
}

func TestTitleMentionsKeepsARangeARange(t *testing.T) {
	got := TitleMentions("AGILE Reproducibility Reviews 2025 (2025-008 - 2025-017) and 2025-020")
	if want := []string{"2025-008 - 2025-017", "2025-020"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTitleWithCertificate(t *testing.T) {
	cases := []struct{ title, id, want string }{
		{"Baetzel", "2026-020", "Baetzel | 2026-020"},
		{"Baetzel | 2026-020", "2026-027", "Baetzel | 2026-027"},
		{"Baetzel|2026-020", "2026-027", "Baetzel| 2026-027"},
		// A bar that is part of the names is not the identifier's place.
		{"Smith | Jones", "2026-027", "Smith | Jones | 2026-027"},
		{"", "2026-027", "2026-027"},
	}
	for _, testCase := range cases {
		if got := TitleWithCertificate(testCase.title, testCase.id); got != testCase.want {
			t.Errorf("%q with %s: got %q, want %q", testCase.title, testCase.id, got, testCase.want)
		}
	}
}

// The measurement the launch-pad was written for: register.csv stops at
// 2026-023 while open issues already hold 024 to 026.
func TestNextCertificateTakesBothSources(t *testing.T) {
	claims := []Claim{
		{ID: "2025-028"}, {ID: "2026-019"}, {ID: "2026-023"},
		{ID: "2026-024", Issue: 203}, {ID: "2026-026", Issue: 214}, {ID: "2026-025", Issue: 211},
		{ID: "2027-001", Issue: 300}, {ID: "not-an-id"},
	}
	next, highest, found := NextCertificate(2026, claims)
	if next != "2026-027" || !found || highest != (Claim{ID: "2026-026", Issue: 214}) {
		t.Errorf("got %s from %+v (%v)", next, highest, found)
	}

	// The register is named rather than the issue when both hold the highest.
	claims = append(claims, Claim{ID: "2026-026"})
	if _, highest, _ := NextCertificate(2026, claims); !highest.InRegister() {
		t.Errorf("the highest should be the register's row, got %+v", highest)
	}
}

func TestNextCertificateStartsTheYear(t *testing.T) {
	next, _, found := NextCertificate(2027, []Claim{{ID: "2026-040"}})
	if next != "2027-001" || found {
		t.Errorf("got %s (%v)", next, found)
	}
}

// Measured from the identifier below, as the R package does: a certificate
// published late after higher ones is not a jump.
func TestCertificateJump(t *testing.T) {
	claims := []Claim{{ID: "2026-001"}, {ID: "2026-002"}, {ID: "2026-030", Issue: 7}}
	cases := []struct {
		id       string
		previous string
		gap      int
	}{
		{"2026-011", "2026-002", 9},
		{"2026-012", "2026-002", 10},
		{"2026-003", "2026-002", 1},
		{"2026-040", "2026-030", 10},
		{"2027-009", "", 9},
		{"2027-010", "", 10},
	}
	for _, testCase := range cases {
		previous, gap, found := CertificateJump(testCase.id, claims)
		if previous.ID != testCase.previous || gap != testCase.gap || found != (testCase.previous != "") {
			t.Errorf("%s: got %+v, %d, %v", testCase.id, previous, gap, found)
		}
	}
}

func TestCertificateTaken(t *testing.T) {
	claims := []Claim{{ID: "2026-024", Issue: 203}, {ID: "2026-023", Issue: 199}, {ID: "2026-023"}}

	if claim, taken := CertificateTaken("2026-023", claims); !taken || !claim.InRegister() {
		t.Errorf("the register's row should be named first, got %+v", claim)
	}
	if claim, taken := CertificateTaken("2026-024", claims); !taken || claim.Issue != 203 {
		t.Errorf("an issue's claim should be named, got %+v", claim)
	}
	if _, taken := CertificateTaken("2026-099", claims); taken {
		t.Error("an unclaimed identifier is free")
	}
}
