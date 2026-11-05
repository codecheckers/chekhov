package bot

import (
	"strings"

	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/people"
)

// landingPage is what `check finish` says about the register's page for the
// certificate the configuration names.
//
// Only a registered certificate has a page to hold to account: the row is
// added by the register pull request, which comes after `check finish`, and
// the page by rendering the register after that. So a certificate that is not
// in `register.csv` yet is told where its page will be, and one that is gets
// a missing or disagreeing page as a failure. The page is read through its
// index.json, which the register renders beside it from the same
// codecheck.yml, and which `announce` reads too.
func (s *Server) landingPage(configuration check.Config, reading people.Reading,
	services *check.Services) string {
	certificate := strings.TrimSpace(configuration.Certificate)
	if !check.IsCertificateID(certificate) {
		// Missing or malformed: CC-CFG-026 has said so already, and a value
		// that is not an identifier is not put into a URL, or into the reply.
		return ""
	}
	if recorded := reading.Record.Certificate; reading.Tampered == nil && recorded != "" &&
		recorded != certificate {
		// A file copied from another check names that check's certificate:
		// its page would be compared, and the mismatch never said.
		return command.LandingPageOtherCertificate(certificate, recorded)
	}
	page := s.Settings.CertificateURL(certificate)
	_, registered, err := services.RowOfCertificate(certificate)
	switch {
	case err != nil:
		return command.LandingPageUnknown(certificate, err)
	case !registered:
		return command.LandingPageLater(certificate, page)
	}

	published, err := services.PublishedAt(page)
	switch {
	case check.IsNotFound(err):
		exists, err := services.PageExists(page)
		switch {
		case err != nil:
			return command.LandingPageUnknown(certificate, err)
		case exists:
			return command.LandingPageUncompared(certificate, page)
		}
		return command.LandingPageMissing(certificate, page)
	case err != nil:
		return command.LandingPageUnknown(certificate, err)
	}
	found := published.Differences(configuration)
	if len(found) == 0 {
		return command.LandingPage(certificate, page)
	}
	differences := make([]command.Difference, len(found))
	for i, d := range found {
		differences[i] = command.Difference(d)
	}
	return command.LandingPageDiffers(certificate, page, differences)
}
