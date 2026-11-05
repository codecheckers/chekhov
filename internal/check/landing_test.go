package check

import (
	"reflect"
	"testing"
)

func TestDifferencesBetweenAPageAndItsConfiguration(t *testing.T) {
	configuration := Config{
		Certificate: "1970-001",
		Paper:       Paper{Title: "A paper, whose code runs", Reference: "https://doi.org/10.5555/preprint.1"},
		Report:      "https://doi.org/10.5281/zenodo.1234567",
		Codechecker: []Person{{Name: "S. Eglen", ORCID: "0000-0001-8607-8025"}},
	}
	agreeing := Published{
		ID:             "1970-001",
		PaperTitle:     "A paper whose code runs",
		PaperReference: "doi:10.5555/PREPRINT.1",
		Report:         "10.5281/zenodo.1234567",
		Codecheckers:   []Person{{Name: "s eglen", ORCID: "https://orcid.org/0000-0001-8607-8025"}},
	}
	if got := agreeing.Differences(configuration); len(got) != 0 {
		t.Errorf("a page written differently but saying the same was found to differ: %+v", got)
	}

	for _, c := range []struct {
		name   string
		change func(*Published)
		want   []string
	}{
		{"another certificate", func(p *Published) { p.ID = "1970-002" }, []string{"the certificate"}},
		{"another reference", func(p *Published) { p.PaperReference = "https://doi.org/10.5555/other" },
			[]string{"the paper's reference"}},
		{"no report", func(p *Published) { p.Report = "" }, []string{"the report"}},
		{"another ORCID", func(p *Published) { p.Codecheckers[0].ORCID = "0000-0002-1825-0097" },
			[]string{"the codecheckers"}},
		// A page that gives no ORCID does not contradict a file that does.
		{"no ORCID", func(p *Published) { p.Codecheckers[0].ORCID = "" }, nil},
		{"a second codechecker", func(p *Published) {
			p.Codecheckers = append(p.Codecheckers, Person{Name: "Somebody Else"})
		}, []string{"the codecheckers"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			page := agreeing
			page.Codecheckers = append([]Person(nil), agreeing.Codecheckers...)
			c.change(&page)
			var got []string
			for _, d := range page.Differences(configuration) {
				got = append(got, d.What)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("differences %v, want %v", got, c.want)
			}
		})
	}
}
