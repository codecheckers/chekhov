package command

import (
	"fmt"
	"strings"
)

// What `check finish` says about the register's page for the certificate.
//
// The page is the last thing a check produces and the first a reader finds,
// so once the certificate is in `register.csv` a page that is missing, or
// says something else than the configuration, is a failure. Before then it
// is not: the page comes from rendering the register after the pull request
// that adds the row is merged, which is the step `check finish` points at.

// A Difference is one thing the page says otherwise than `codecheck.yml`:
// what it is about, and what each says. check.Difference converts to it.
type Difference struct {
	What, Page, File string
}

// LandingPage is the page agreeing with the configuration: the link, said
// loudly, because it is what the codechecker and the authors share.
func LandingPage(certificate, page string) string {
	return fmt.Sprintf("✔ **[CODECHECK certificate %s in the register](%s)**\n", certificate, page)
}

// LandingPageDiffers is the page saying something else than the
// configuration about the things a reader looks for first.
func LandingPageDiffers(certificate, page string, differences []Difference) string {
	var out strings.Builder
	fmt.Fprintf(&out, "✖ **[The register's page for certificate %s](%s)** does not say what "+
		"`codecheck.yml` says:\n\n", certificate, page)
	for _, d := range differences {
		fmt.Fprintf(&out, "- %s: the page says %s, `codecheck.yml` says %s\n",
			d.What, valueOrNothing(d.Page), valueOrNothing(d.File))
	}
	out.WriteString("\nThe page is rendered from the `codecheck.yml` the register was given: correct " +
		"whichever is wrong, and render the register again.\n")
	return out.String()
}

// LandingPageMissing is a certificate the register lists with no page.
func LandingPageMissing(certificate, page string) string {
	return fmt.Sprintf("✖ **`register.csv` lists certificate %s, and its page %s does not exist.** "+
		"The register has to be rendered again for it to appear.\n", certificate, Code(page))
}

// LandingPageLater is a certificate not in the register yet: its page comes
// after the pull request, and is checked when `check finish` runs again.
func LandingPageLater(certificate, page string) string {
	return fmt.Sprintf("ℹ Certificate %s is not in `register.csv` yet, so it has no page. Once the "+
		"register pull request is merged and the register rendered, it will be at %s; "+
		"run `%s check finish` again then, and I will check that it says what `codecheck.yml` says.\n",
		certificate, Code(page), Bot)
}

// LandingPageUnknown is a page nobody could look at: not a failure.
func LandingPageUnknown(certificate string, err error) string {
	return fmt.Sprintf("… I could not check the register's page for certificate %s: %s\n",
		certificate, Sentence(err.Error()))
}

// LandingPageUncompared is a page that is there without the index.json the
// comparison reads: published, and not compared.
func LandingPageUncompared(certificate, page string) string {
	return fmt.Sprintf("… **[The register's page for certificate %s](%s)** is there, without the "+
		"`index.json` I compare with `codecheck.yml`, so I have not compared them.\n", certificate, page)
}

// LandingPageOtherCertificate is a configuration naming another certificate
// than the record of the check: which page to read is the question.
func LandingPageOtherCertificate(file, recorded string) string {
	return fmt.Sprintf("✖ **`codecheck.yml` names certificate %s, and the record of this check %s**, "+
		"so I have not looked at the register's page. Correct whichever is wrong.\n", file, recorded)
}

// valueOrNothing is a value as a difference shows it: in a code span, so that
// nothing in a title is rendered, on one line, and defused - the page's text
// is somebody else's, and goes into a comment of the bot's.
func valueOrNothing(value string) string {
	value = Defuse(strings.Join(strings.Fields(value), " "))
	if value == "" {
		return "nothing"
	}
	return Code(value)
}
