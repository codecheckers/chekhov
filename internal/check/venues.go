package check

import (
	"slices"
	"strings"
)

// venue is a row of venues.csv: the name register.csv uses, and the label,
// which is the venue's type for most venues and an issue label for a few.
type venue struct {
	Name  string
	Label string
}

// venues are the rows of venues.csv. What an issue's labels say about them is
// CC-REG-008's business, and #21 reads them the other way round to fill in a
// row from the labels: both go through here, so the two cannot disagree.
type venues []venue

// find looks a venue up by name, ignoring case.
func (vs venues) find(name string) (venue, bool) {
	i := slices.IndexFunc(vs, func(v venue) bool { return strings.EqualFold(v.Name, name) })
	if i < 0 {
		return venue{}, false
	}
	return vs[i], true
}

// names are the names of the venues that match.
func (vs venues) names(match func(venue) bool) []string {
	var names []string
	for _, v := range vs {
		if match(v) {
			names = append(names, v.Name)
		}
	}
	return names
}

// typed are the venues whose label in venues.csv is the given type.
func (vs venues) typed(venueType string) []string {
	return vs.names(func(v venue) bool { return strings.EqualFold(v.Label, venueType) })
}

// named are the venues an issue's labels name. A label naming one venue - its
// name, or its label in venues.csv where that is not a Type, as check-nl is -
// wins; otherwise a type label names the venue when exactly one venue carries
// that type as its label, as community names codecheck.
func (vs venues) named(labels []string) []string {
	var named []string
	for _, label := range labels {
		named = append(named, vs.names(func(v venue) bool {
			return strings.EqualFold(v.Name, label) || strings.EqualFold(v.Label, label) && !IsVenueType(label)
		})...)
	}
	if len(named) == 0 {
		for _, label := range typeLabels(labels) {
			if typed := vs.typed(label); len(typed) == 1 {
				named = append(named, typed[0])
			}
		}
	}
	// One venue is one name, however venues.csv happens to spell it twice.
	slices.SortFunc(named, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
	return slices.CompactFunc(named, strings.EqualFold)
}

// labelsFor are the labels an issue needs for a row: its Type, and the
// venue's own label unless the Type alone names the venue. A venue venues.csv
// does not list has no label to want, which is CC-REG-005's finding.
func (vs venues) labelsFor(venueType, name string) []string {
	wanted := []string{venueType}
	v, listed := vs.find(name)
	if !listed || slices.Equal(vs.named(wanted), []string{v.Name}) {
		return wanted
	}
	if v.Label != "" && !IsVenueType(v.Label) {
		return append(wanted, v.Label)
	}
	return append(wanted, v.Name)
}

// typeLabels are the labels that are a Type, in the issue's order.
func typeLabels(labels []string) []string {
	var types []string
	for _, label := range labels {
		if IsVenueType(label) {
			types = append(types, label)
		}
	}
	return types
}

// IsVenueType reports whether a word is one of the values the Type column
// may take, ignoring case.
func IsVenueType(word string) bool { return ContainsFold(knownVenueTypes, word) }

// Labelled is what an issue's labels say of its register row.
type Labelled struct {
	// Types are the labels that are a Type. A row has one.
	Types []string
	// Venues are the venues the labels name, as venues.csv spells them. A row
	// has one.
	Venues []string
}

// RowLabels reads an issue's labels the way CC-REG-008 reads them, so that a
// row filled in from them is a row the rule passes (#21). Without venues.csv
// only the types can be read, and the error says why.
func (s *Services) RowLabels(labels []string) (Labelled, error) {
	read := Labelled{Types: typeLabels(labels)}
	register, err := s.registerCSV()
	if err != nil {
		return read, err
	}
	read.Venues = register.venues.named(labels)
	return read, nil
}

// Venue is a venue as venues.csv spells it, looked up ignoring case. listed
// is false for a venue venues.csv does not have, which CC-REG-005 warns about
// and does not refuse.
func (s *Services) Venue(name string) (canonical string, listed bool, err error) {
	register, err := s.registerCSV()
	if err != nil {
		return name, false, err
	}
	if v, found := register.venues.find(name); found {
		return v.Name, true, nil
	}
	return name, false, nil
}
