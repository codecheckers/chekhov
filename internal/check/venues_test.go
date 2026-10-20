package check

import (
	"slices"
	"strings"
	"testing"
)

// The labels CC-REG-008 asks for are labels it accepts: for every venue of the
// production register's venues.csv, the labels wanted for its row name it and
// nothing else. The verdict and the hint are two directions of one reading,
// and this is what keeps them from drifting apart.
func TestTheLabelsWantedNameTheVenue(t *testing.T) {
	register := venues{
		{"GigaScience", "journal"}, {"J Geogr Syst", "journal"}, {"JOSIS", "journal"},
		{"AGILEGIS", "conference"}, {"codecheck", "community"}, {"codecheck NL", "check-nl"},
		{"preprint", "preprint"}, {"in press", "journal"}, {"AUMC", "institution"},
		{"Lifecycle Journal", "lifecycle journal"}, {"TU Delft DCC", "institution"},
	}
	rows := []struct{ venueType, venue, wants string }{
		{"journal", "GigaScience", "journal GigaScience"},
		{"journal", "Lifecycle Journal", "journal lifecycle journal"},
		{"conference", "AGILEGIS", "conference"},
		{"community", "codecheck", "community"},
		{"community", "codecheck NL", "community check-nl"},
		{"community", "preprint", "community preprint"},
		{"institution", "TU Delft DCC", "institution TU Delft DCC"},
	}
	for _, row := range rows {
		labels := register.labelsFor(row.venueType, row.venue)
		if got := strings.Join(labels, " "); got != row.wants {
			t.Errorf("%s, %s wants %q, want %q", row.venueType, row.venue, got, row.wants)
		}
		if named := register.named(labels); !slices.Equal(named, []string{row.venue}) {
			t.Errorf("%s, %s: the labels %v it wants name %v", row.venueType, row.venue, labels, named)
		}
	}
}
