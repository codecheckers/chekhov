package check

import (
	"context"
	"net/http"
	"testing"
)

// What a registration reads off an ORCID record: the public emails, the
// researcher's own links, and the difference between an iD ORCID does not
// know and ORCID not answering (codecheckers/chekhov#49).
func TestORCIDProfileReadsEmailsAndPages(t *testing.T) {
	s := newStub(t)
	s.JSON("/orcid/0000-0002-1825-0097/person", `{
	  "name": {"given-names": {"value": "Josiah"}, "family-name": {"value": "Carberry"}},
	  "emails": {"email": [{"email": "made-up@example.invalid", "verified": true},
	    {"email": "made-up-too@example.invalid", "verified": false}, {"email": " "}]},
	  "researcher-urls": {"researcher-url": [{"url-name": "Home", "url": {"value": "https://example.invalid/carberry"}}]}
	}`)
	s.JSON("/orcid/0000-0001-8607-8025/person", orcidPersonJSON)
	s.Status("/orcid/0000-0002-9079-593X/person", http.StatusServiceUnavailable)
	ctx := context.Background()

	profile, err := s.services.ORCIDProfile(ctx, "0000-0002-1825-0097")
	if err != nil {
		t.Fatal(err)
	}
	if profile.Name != "Josiah Carberry" || len(profile.Emails) != 2 || profile.Unverified != 1 || len(profile.Pages) != 1 {
		t.Errorf("profile = %+v, want the name, the two addresses - one unverified - and the one page", profile)
	}

	// A record without the two fields has neither, which is not an error.
	if profile, err := s.services.ORCIDProfile(ctx, "0000-0001-8607-8025"); err != nil || len(profile.Emails) != 0 {
		t.Errorf("a record with no emails: %+v, %v", profile, err)
	}
	if _, err := s.services.ORCIDProfile(ctx, "0000-0003-1419-2405"); !IsNotFound(err) {
		t.Errorf("an iD ORCID does not know: %v, want not found", err)
	}
	if _, err := s.services.ORCIDProfile(ctx, "0000-0002-9079-593X"); err == nil || IsNotFound(err) {
		t.Errorf("ORCID answering 503: %v, want an error that is not 'not found'", err)
	}
	if _, err := (*Services)(nil).ORCIDProfile(ctx, "0000-0002-1825-0097"); err == nil {
		t.Error("an offline run read a record")
	}
}

func TestORCIDWellFormed(t *testing.T) {
	for orcid, want := range map[string]bool{
		"0000-0002-1825-0097":                   true,
		"0000-0002-9079-593X":                   true,
		"0000-0002-1825-0098":                   false,
		"https://orcid.org/0000-0002-1825-0097": false,
		"0000-0002-1825-009":                    false,
	} {
		if got := ORCIDWellFormed(orcid); got != want {
			t.Errorf("ORCIDWellFormed(%q) = %v, want %v", orcid, got, want)
		}
	}
}
