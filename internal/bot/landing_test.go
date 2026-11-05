package bot

import (
	"net/http"
	"strings"
	"testing"

	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/people"
	"github.com/codecheckers/chekhov/internal/testserver"
)

// `check finish` and the register's page for the certificate: checked once the
// certificate is in register.csv, pointed at before then.

// publishedPage is index.json for testdata/valid-2.0, written the way the
// register writes one: a bare DOI here, an ORCID as a URL there, a title
// broken over lines.
const publishedPage = `{
  "certificate": {"id": "1970-001"},
  "paper": {"title": "A paper whose code runs\n", "reference": "10.5555/PREPRINT.1"},
  "codecheck": {
    "codecheckers": [{"name": "S. Eglen", "orcid": "https://orcid.org/0000-0001-8607-8025"}],
    "report": "https://doi.org/10.5281/zenodo.1234567"
  }
}`

// finishingWithAPage is a clean check of testdata/valid-2.0, certificate
// 1970-001, in the register or not, its page answering as given: a body, or a
// status when the body is empty.
func finishingWithAPage(t *testing.T, registered bool, page string, status int) (*Server, string) {
	server, url, _ := finishingWithAStub(t, registered, page, status)
	return server, url
}

// finishingWithAStub is finishingWithAPage, with the stub, for a test that
// serves more.
func finishingWithAStub(t *testing.T, registered bool, page string, status int) (*Server, string, *testserver.Server) {
	t.Helper()
	server, path := checking(t, "valid-2.0", "work in progress")
	copyArchived(path.stub, true)
	csv := "Certificate,Repository,Type,Venue,Issue\n"
	if registered {
		csv += "1970-001,github::codecheckers/demo,journal,FAKE,1\n"
	}
	path.stub.Text("/raw/"+server.Settings.TargetRepository()+"/HEAD/register.csv", csv)
	server.Settings.Chekhov.Env.Certificates = path.stub.URL + "/certs/{id}/"
	if page != "" {
		path.stub.JSON("/certs/1970-001/index.json", page)
	} else if status != 0 {
		path.stub.Status("/certs/1970-001/index.json", status)
	}
	return server, path.stub.URL + "/certs/1970-001/", path.stub
}

func finish(t *testing.T, server *Server) string {
	t.Helper()
	return answer(t, server, command.CheckFinish, []string{"github::codecheckers/demo"}, "")
}

func TestCheckFinishLinksTheRegistersPage(t *testing.T) {
	server, page := finishingWithAPage(t, true, publishedPage, 0)

	reply := finish(t, server)
	if want := "✔ **[CODECHECK certificate 1970-001 in the register](" + page + ")**"; !strings.Contains(reply, want) {
		t.Errorf("the reply does not link the page as %q:\n%s", want, reply)
	}
}

func TestCheckFinishFailsAPageThatSaysSomethingElse(t *testing.T) {
	stale := strings.NewReplacer("A paper whose code runs", "An earlier title",
		"zenodo.1234567", "zenodo.7654321").Replace(publishedPage)
	server, _ := finishingWithAPage(t, true, stale, 0)

	reply := finish(t, server)
	for _, want := range []string{
		"✖ **[The register's page for certificate 1970-001]",
		"the paper's title: the page says `An earlier title`, `codecheck.yml` says `A paper whose code runs`",
		"the report: the page says `https://doi.org/10.5281/zenodo.7654321`",
		"render the register again",
	} {
		if !strings.Contains(reply, want) {
			t.Errorf("the reply does not say %q:\n%s", want, reply)
		}
	}
	for _, agreed := range []string{"the certificate:", "the paper's reference:", "the codecheckers:"} {
		if strings.Contains(reply, agreed) {
			t.Errorf("the reply lists %q, which the page agrees on:\n%s", agreed, reply)
		}
	}
}

func TestCheckFinishFailsARegisteredCertificateWithoutAPage(t *testing.T) {
	server, page := finishingWithAPage(t, true, "", http.StatusNotFound)

	reply := finish(t, server)
	if want := "✖ **`register.csv` lists certificate 1970-001, and its page `" + page + "` does not exist.**"; !strings.Contains(reply, want) {
		t.Errorf("the reply does not say %q:\n%s", want, reply)
	}
}

// The usual case: `check finish` comes before the register pull request, so
// there is no page yet, and that is not a failure.
func TestCheckFinishPointsAtThePageOfACertificateNotRegisteredYet(t *testing.T) {
	server, page := finishingWithAPage(t, false, "", 0)

	reply := finish(t, server)
	if want := "ℹ Certificate 1970-001 is not in `register.csv` yet, so it has no page"; !strings.Contains(reply, want) {
		t.Errorf("the reply does not say %q:\n%s", want, reply)
	}
	if !strings.Contains(reply, "`"+page+"`") {
		t.Errorf("the reply does not say where the page will be:\n%s", reply)
	}
}

// A page nobody could read is "could not check", never a failure.
func TestCheckFinishSaysAnUnreadablePageCouldNotBeChecked(t *testing.T) {
	server, _ := finishingWithAPage(t, true, "", http.StatusBadGateway)

	reply := finish(t, server)
	if !strings.Contains(reply, "… I could not check the register's page for certificate 1970-001") {
		t.Errorf("the reply does not say the page could not be checked:\n%s", reply)
	}
	if strings.Contains(reply, "✖ **") {
		t.Errorf("an unreadable page was reported as a failure:\n%s", reply)
	}
}

// A check waiting on metadata is not registered, and its page is not asked about.
func TestCheckFinishLeavesThePageOfAPendingCheck(t *testing.T) {
	server, path := checking(t, "placeholders", "work in progress")
	inTheRegister(path.stub, server, false)

	if reply := finish(t, server); strings.Contains(reply, "register's page") ||
		strings.Contains(reply, "has no page") {
		t.Errorf("a pending check was told about the register's page:\n%s", reply)
	}
}

// A certificate that is not an identifier goes into neither a URL nor the
// reply: CC-CFG-026 reports it.
func TestTheRegistersPageOfAMalformedCertificateIsNotAskedFor(t *testing.T) {
	server, _ := finishingWithAPage(t, true, publishedPage, 0)

	for _, certificate := range []string{"", "@a-made-up-author", "../../elsewhere", "1970-001]](x)"} {
		if said := server.landingPage(check.Config{Certificate: certificate}, people.Reading{},
			server.Services); said != "" {
			t.Errorf("certificate %q was answered: %s", certificate, said)
		}
	}
}

// A configuration copied from another check names that check's certificate,
// and its page is not the one to compare.
func TestTheRegistersPageWaitsForTheRecordToAgree(t *testing.T) {
	server, _ := finishingWithAPage(t, true, publishedPage, 0)

	said := server.landingPage(check.Config{Certificate: "1970-001"},
		people.Reading{Record: people.Record{Certificate: "1970-002"}}, server.Services)
	if want := "✖ **`codecheck.yml` names certificate 1970-001, and the record of this check 1970-002**"; !strings.Contains(said, want) {
		t.Errorf("the reply does not say %q:\n%s", want, said)
	}
}

// A page rendered before the register wrote index.json is published, and
// not compared: never a missing page.
func TestARegistersPageWithoutIndexJSONIsNotCompared(t *testing.T) {
	server, _, stub := finishingWithAStub(t, true, "", http.StatusNotFound)
	stub.Text("/certs/1970-001/", "<html>certificate 1970-001</html>")

	said := server.landingPage(check.Config{Certificate: "1970-001"}, people.Reading{}, server.Services)
	if !strings.Contains(said, "is there, without the `index.json`") || strings.Contains(said, "✖") {
		t.Errorf("a page without index.json was not reported as uncompared:\n%s", said)
	}
}
