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
			return strings.EqualFold(v.Name, label) || strings.EqualFold(v.Label, label) && !isVenueType(label)
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
	if v.Label != "" && !isVenueType(v.Label) {
		return append(wanted, v.Label)
	}
	return append(wanted, v.Name)
}

// typeLabels are the labels that are a Type, in the issue's order.
func typeLabels(labels []string) []string {
	var types []string
	for _, label := range labels {
		if isVenueType(label) {
			types = append(types, label)
		}
	}
	return types
}

func isVenueType(word string) bool { return ContainsFold(knownVenueTypes, word) }
