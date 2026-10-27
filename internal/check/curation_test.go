package check

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// invenioJSON is stubConfig's certificate as InvenioRDM writes it, following
// every item of the curation policy.
const invenioJSON = `{
  "id": "1234567",
  "is_draft": false,
  "pids": {"doi": {"identifier": "10.5281/zenodo.1234567"}},
  "parent": {
    "pids": {"doi": {"identifier": "10.5281/zenodo.1234566"}},
    "communities": {"ids": ["505e49f2-de4f-4905-9666-36c7303e497d"]}
  },
  "versions": {"index": 1, "is_latest": true},
  "files": {"order": [], "entries": {
    "codecheck.pdf": {"key": "codecheck.pdf"},
    "codecheck.Rmd": {"key": "codecheck.Rmd"}
  }},
  "metadata": {
    "title": "CODECHECK Certificate 2026-001",
    "description": "<p>All outputs reproduced.</p>",
    "publisher": "CODECHECK Community on Zenodo",
    "resource_type": {"id": "publication-report"},
    "languages": [{"id": "eng"}],
    "rights": [{"id": "cc-by-4.0"}, {"id": "mit"}],
    "creators": [{"person_or_org": {"type": "personal", "name": "Eglen, Stephen J.",
      "identifiers": [{"identifier": "0000-0001-8607-8025", "scheme": "orcid"}]}}],
    "related_identifiers": [
      {"identifier": "10.5555/preprint.1", "scheme": "doi", "relation_type": {"id": "reviews"}},
      {"identifier": "https://github.com/codecheckers/demo", "scheme": "url", "relation_type": {"id": "isderivedfrom"}}
    ],
    "identifiers": [
      {"identifier": "http://cdchck.science/register/certs/2026-001", "scheme": "url"},
      {"identifier": "cdchck.science/register/certs/2026-001", "scheme": "other"}
    ]
  }
}`

// asDraft is the record unpublished: no community yet but a request to join
// it, no version, and its files listed beside it rather than in it.
func asDraft(record string) string {
	record = strings.Replace(record, `"is_draft": false`, `"is_draft": true`, 1)
	record = strings.Replace(record, `"communities": {"ids": ["505e49f2-de4f-4905-9666-36c7303e497d"]}`,
		`"communities": {}, "review": {"status": "submitted", "receiver": {"community": "505e49f2-de4f-4905-9666-36c7303e497d"}}`, 1)
	record = strings.Replace(record, `"versions": {"index": 1, "is_latest": true}`, `"versions": {}`, 1)
	return strings.Replace(record, `"entries": {
    "codecheck.pdf": {"key": "codecheck.pdf"},
    "codecheck.Rmd": {"key": "codecheck.Rmd"}
  }`, `"enabled": true`, 1)
}

// secret stands for a share link's token, which must never be repeated.
const secret = "s3cr3t-t0ken"

// withToken answers only a request carrying the share link's token, as
// Zenodo answers a draft, and 404 otherwise.
func withToken(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != secret {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

// invenioOnly answers only a request for InvenioRDM's representation, which
// is the one that carries the publisher and the community.
func invenioOnly(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != invenioMediaType {
			http.Error(w, "the default representation", http.StatusNotAcceptable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

func TestParseZenodoTarget(t *testing.T) {
	cases := []struct {
		word string
		want ZenodoTarget
		err  error
	}{
		{"10.5281/zenodo.1234567", ZenodoTarget{ID: "1234567", DOI: "10.5281/zenodo.1234567"}, nil},
		{"https://doi.org/10.5281/zenodo.1234567", ZenodoTarget{ID: "1234567", DOI: "10.5281/zenodo.1234567"}, nil},
		{"10.5072/zenodo.42", ZenodoTarget{ID: "42", Sandbox: true, DOI: "10.5072/zenodo.42"}, nil},
		{"https://zenodo.org/records/1234567", ZenodoTarget{ID: "1234567"}, nil},
		{"https://zenodo.org/record/1234567/", ZenodoTarget{ID: "1234567"}, nil},
		{"<https://zenodo.org/records/1234567?token=" + secret + ">", ZenodoTarget{ID: "1234567", Token: secret}, nil},
		{"https://zenodo.org/records/1234567?preview=1&token=" + secret,
			ZenodoTarget{ID: "1234567", Token: secret, Preview: true}, nil},
		{"https://zenodo.org/uploads/1234567", ZenodoTarget{ID: "1234567"}, nil},
		{"https://zenodo.org/doi/10.5281/zenodo.1234567", ZenodoTarget{ID: "1234567", DOI: "10.5281/zenodo.1234567"}, nil},
		{"https://sandbox.zenodo.org/records/42?token=" + secret, ZenodoTarget{ID: "42", Sandbox: true, Token: secret}, nil},
		{"10.17605/OSF.IO/ABCDE", ZenodoTarget{}, ErrOutOfScope},
		{"https://osf.io/abcde", ZenodoTarget{}, ErrOutOfScope},
		{"10.53962/abcd-1234", ZenodoTarget{}, ErrOutOfScope},
	}
	for _, c := range cases {
		got, err := ParseZenodoTarget(c.word)
		if !errors.Is(err, c.err) || got != c.want {
			t.Errorf("ParseZenodoTarget(%q) = %+v, %v; want %+v, %v", c.word, got, err, c.want, c.err)
		}
	}
	for _, word := range []string{"https://zenodo.org/communities/codecheck", "10.5555/preprint.1", "codecheckers/demo"} {
		if _, err := ParseZenodoTarget(word); err == nil || errors.Is(err, ErrOutOfScope) {
			t.Errorf("ParseZenodoTarget(%q) = %v, want an error that is not out of scope", word, err)
		}
	}
	if got := (ZenodoTarget{ID: "1", Token: secret}).String(); strings.Contains(got, secret) {
		t.Errorf("String() = %q, which repeats the token", got)
	}
}

// audit audits stubConfig's record, changed by edit, against stubConfig.
func (s *stub) audit(t *testing.T, target ZenodoTarget) ZenodoAudit {
	t.Helper()
	s.services.reset()
	return AuditZenodo(context.Background(), target, s.context(t, stubConfig, ""), s.services)
}

func statuses(audit ZenodoAudit) map[string]Status {
	got := map[string]Status{}
	for _, item := range audit.Policy {
		got[item.What] = item.Status
	}
	return got
}

func TestAuditOfARecordInGoodOrder(t *testing.T) {
	s := newStub(t)
	s.Handle("/zenodo/records/1234567", invenioOnly(invenioJSON))
	audit := s.audit(t, ZenodoTarget{ID: "1234567", DOI: "10.5281/zenodo.1234567"})
	if audit.Problem != "" {
		t.Fatalf("Problem = %q", audit.Problem)
	}
	if audit.Draft || audit.Shared {
		t.Errorf("Draft, Shared = %v, %v, want a published record read without a link", audit.Draft, audit.Shared)
	}
	for what, status := range statuses(audit) {
		if status != StatusPass {
			t.Errorf("%s: %s, want pass", what, status)
		}
	}
	if len(audit.Policy) != 14 {
		t.Errorf("%d policy items, want the 13 of the policy and the report", len(audit.Policy))
	}
	// A rule shown with the policy is counted there, not again beneath it.
	if want := fmt.Sprintf("%d passed, 0 failed", len(audit.Rules)); !strings.HasPrefix(audit.RulesSummary, want) {
		t.Errorf("RulesSummary = %q, want it to count the %d rules listed beneath the policy", audit.RulesSummary, len(audit.Rules))
	}
	for _, rule := range audit.Rules {
		if rule.Outcome != OutcomeOK {
			t.Errorf("%s: %s %s, want ok", rule.Rule.ID, rule.Outcome, rule.Detail)
		}
		if rule.Rule.ID == "CC-REP-007" || rule.Rule.ID == "CC-REP-009" {
			t.Errorf("%s is a policy item and is listed among the rules again", rule.Rule.ID)
		}
	}
}

// Each policy item, broken one at a time.
func TestAuditFindsWhatThePolicyAsksFor(t *testing.T) {
	cases := []struct {
		item, from, to string
	}{
		{"title", `"title": "CODECHECK Certificate 2026-001"`, `"title": "Certificate 2026-001"`},
		{"description", `"description": "<p>All outputs reproduced.</p>"`, `"description": ""`},
		{"licence", `[{"id": "cc-by-4.0"}, {"id": "mit"}]`, `[{"id": "mit"}]`},
		{"resource type", `"publication-report"`, `"publication-other"`},
		{"publisher", `"CODECHECK Community on Zenodo"`, `"Zenodo"`},
		{"language", `[{"id": "eng"}]`, `[]`},
		{"the certificate PDF (CC-REP-007)", `"codecheck.pdf": {"key": "codecheck.pdf"},`, ``},
		{"a machine-readable source of the certificate (CC-REP-009)", `,
    "codecheck.Rmd": {"key": "codecheck.Rmd"}`, ``},
		{"a Reviews relation to the paper", `"relation_type": {"id": "reviews"}`, `"relation_type": {"id": "cites"}`},
		{"a Reviews relation to the paper", `"identifier": "10.5555/preprint.1"`, `"identifier": "10.5555/another.paper"`},
		{"a relation to the repository in `codecheckers/` or `cdchck`", `github.com/codecheckers/demo`, `github.com/someone/demo`},
		{"the alternate identifier `http://cdchck.science/register/certs/<ID>` as url",
			`"http://cdchck.science/register/certs/2026-001"`, `"http://cdchck.science/register/certs/2026-002"`},
		{"the alternate identifier `cdchck.science/register/certs/<ID>` as other", `"scheme": "other"`, `"scheme": "url"`},
		{"in the CODECHECK community", `"ids": ["505e49f2-de4f-4905-9666-36c7303e497d"]`, `"ids": []`},
		{"the codecheck.yml's report", `"identifier": "10.5281/zenodo.1234567"`, `"identifier": "10.5281/zenodo.7654321"`},
	}
	for _, c := range cases {
		t.Run(c.item, func(t *testing.T) {
			if !strings.Contains(invenioJSON, c.from) {
				t.Fatalf("the fixture has no %s", c.from)
			}
			s := newStub(t)
			s.Handle("/zenodo/records/1234567", invenioOnly(strings.Replace(invenioJSON, c.from, c.to, 1)))
			audit := s.audit(t, ZenodoTarget{ID: "1234567"})
			got := statuses(audit)
			if got[c.item] != StatusFail {
				t.Errorf("%s: %q, want fail", c.item, got[c.item])
			}
			for what, status := range got {
				if what != c.item && status != StatusPass {
					t.Errorf("%s: %s, want only %s to fail", what, status, c.item)
				}
			}
		})
	}
}

// A draft is read with the share link's token, from the draft's own route,
// and its files from the listing beside it; the rules about the DOI do not
// judge a DOI that is only reserved.
func TestAuditOfADraftByItsShareLink(t *testing.T) {
	s := newStub(t)
	s.Handle("/zenodo/records/1234567/draft", withToken(asDraft(invenioJSON)))
	s.Handle("/zenodo/records/1234567/draft/files", withToken(
		`{"entries": [{"key": "codecheck.pdf"}, {"key": "codecheck.Rmd"}]}`))
	target, err := ParseZenodoTarget("https://zenodo.org/records/1234567?preview=1&token=" + secret)
	if err != nil {
		t.Fatal(err)
	}
	audit := s.audit(t, target)
	if audit.Problem != "" {
		t.Fatalf("Problem = %q", audit.Problem)
	}
	if !audit.Draft || !audit.Shared {
		t.Errorf("Draft, Shared = %v, %v, want a draft read with its link", audit.Draft, audit.Shared)
	}
	if strings.Contains(audit.Record, secret) {
		t.Errorf("Record = %q, which repeats the token", audit.Record)
	}
	if audit.Record != target.String() {
		t.Errorf("Record = %q, want the draft named as it was asked for, %q, not by a DOI that is only reserved",
			audit.Record, target.String())
	}
	for what, status := range statuses(audit) {
		if status != StatusPass {
			t.Errorf("%s: %s, want pass", what, status)
		}
	}
	for _, rule := range audit.Rules {
		switch rule.Rule.ID {
		case "CC-REP-001", "CC-REP-002":
			if rule.Outcome != OutcomeSkipped {
				t.Errorf("%s: %s, want skipped for a draft", rule.Rule.ID, rule.Outcome)
			}
		default:
			if rule.Outcome != OutcomeOK {
				t.Errorf("%s: %s %s, want ok", rule.Rule.ID, rule.Outcome, rule.Detail)
			}
		}
	}
}

// A draft that was not submitted to the community, or whose request will not
// bring it in, fails that item.
func TestADraftNotSubmittedToTheCommunity(t *testing.T) {
	for _, review := range []string{
		`"status": "submitted", "receiver": {"community": "another"}`,
		`"status": "declined", "receiver": {"community": "505e49f2-de4f-4905-9666-36c7303e497d"}`,
		`"status": "expired", "receiver": {"community": "505e49f2-de4f-4905-9666-36c7303e497d"}`,
	} {
		s := newStub(t)
		draft := strings.Replace(asDraft(invenioJSON), `"status": "submitted", "receiver": {"community": "505e49f2-de4f-4905-9666-36c7303e497d"}`,
			review, 1)
		s.Handle("/zenodo/records/1234567/draft", withToken(draft))
		s.Handle("/zenodo/records/1234567/draft/files", withToken(`{"entries": []}`))
		audit := s.audit(t, ZenodoTarget{ID: "1234567", Token: secret})
		if got := statuses(audit)["in the CODECHECK community"]; got != StatusFail {
			t.Errorf("%s: community %s, want fail", review, got)
		}
	}
}

// A share link shows a draft without its request to a community, so a draft
// with none in sight could not be checked, rather than failed.
func TestADraftWhoseRequestIsNotShown(t *testing.T) {
	s := newStub(t)
	draft := strings.Replace(asDraft(invenioJSON), `, "review": {"status": "submitted", "receiver": {"community": "505e49f2-de4f-4905-9666-36c7303e497d"}}`, ``, 1)
	s.Handle("/zenodo/records/1234567/draft", withToken(draft))
	s.Handle("/zenodo/records/1234567/draft/files", withToken(`{"entries": []}`))
	audit := s.audit(t, ZenodoTarget{ID: "1234567", Token: secret})
	if got := statuses(audit)["in the CODECHECK community"]; got != StatusSkip {
		t.Errorf("community: %s, want skip", got)
	}

	// Read any other way, a draft with no request in sight has none.
	s.Handle("/zenodo/records/1234567/draft", invenioOnly(draft))
	s.Handle("/zenodo/records/1234567/draft/files", invenioOnly(`{"entries": []}`))
	audit = s.audit(t, ZenodoTarget{ID: "1234567", Preview: true})
	if got := statuses(audit)["in the CODECHECK community"]; got != StatusFail {
		t.Errorf("community of a draft read without a share link: %s, want fail", got)
	}
}

// A share link to a published record with an edit in progress audits what
// was published, not the edit.
func TestAuditOfARecordWithAnEditInProgress(t *testing.T) {
	s := newStub(t)
	edit := strings.Replace(asDraft(invenioJSON), `"is_draft": true`, `"is_draft": true, "is_published": true`, 1)
	s.Handle("/zenodo/records/1234567/draft", withToken(edit))
	s.Handle("/zenodo/records/1234567", invenioOnly(invenioJSON))
	audit := s.audit(t, ZenodoTarget{ID: "1234567", Token: secret})
	if audit.Problem != "" || audit.Draft {
		t.Errorf("Problem %q, Draft %v; want the published record", audit.Problem, audit.Draft)
	}
}

// A draft whose files cannot be listed says so, rather than going on to a
// published record that does not exist yet.
func TestAuditOfADraftWhoseFilesAreRefused(t *testing.T) {
	s := newStub(t)
	s.Handle("/zenodo/records/1234567/draft", withToken(asDraft(invenioJSON)))
	s.Status("/zenodo/records/1234567/draft/files", http.StatusForbidden)
	s.Status("/zenodo/records/1234567", http.StatusNotFound)
	audit := s.audit(t, ZenodoTarget{ID: "1234567", Token: secret})
	if !strings.Contains(audit.Problem, "403") {
		t.Errorf("Problem = %q, want the files' 403", audit.Problem)
	}
}

// Zenodo answering badly is "could not check", never a failed item, and no
// error repeats the token.
func TestAuditCouldNotReadTheRecord(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusUnauthorized, http.StatusForbidden,
		http.StatusTooManyRequests, http.StatusBadGateway} {
		s := newStub(t)
		s.Status("/zenodo/records/1234567", status)
		s.Status("/zenodo/records/1234567/draft", status)
		audit := s.audit(t, ZenodoTarget{ID: "1234567", Token: secret})
		if audit.Problem == "" || len(audit.Policy) > 0 || len(audit.Rules) > 0 {
			t.Errorf("%d: Problem %q, %d items, %d rules; want a problem and nothing judged",
				status, audit.Problem, len(audit.Policy), len(audit.Rules))
		}
		if strings.Contains(audit.Problem, secret) {
			t.Errorf("%d: Problem %q repeats the token", status, audit.Problem)
		}
		// A record that is not there, or not shown, may be a draft.
		hinted := strings.Contains(audit.Problem, "share link")
		if want := status < 500 && status != http.StatusTooManyRequests; hinted != want {
			t.Errorf("%d: Problem %q, want the hint to use a share link: %v", status, audit.Problem, want)
		}
	}
}

// A connection that fails quotes the URL in Go's own error, token and all.
func TestAuditErrorsLeaveTheTokenOut(t *testing.T) {
	s := newStub(t)
	s.services.Zenodo = "http://unreachable.invalid"
	audit := s.audit(t, ZenodoTarget{ID: "1234567", Token: secret})
	if audit.Problem == "" || strings.Contains(audit.Problem, secret) {
		t.Errorf("Problem = %q, want one that does not repeat the token", audit.Problem)
	}
}

// Without a codecheck.yml, the items that compare with one ask only what the
// policy asks.
func TestAuditWithoutAConfiguration(t *testing.T) {
	s := newStub(t)
	s.Handle("/zenodo/records/1234567", invenioOnly(invenioJSON))
	s.services.reset()
	audit := AuditZenodo(context.Background(), ZenodoTarget{ID: "1234567"}, Context{}, s.services)
	if audit.Problem != "" {
		t.Fatalf("Problem = %q", audit.Problem)
	}
	for what, status := range statuses(audit) {
		if status != StatusPass {
			t.Errorf("%s: %s, want pass", what, status)
		}
	}
	if _, compared := statuses(audit)["the codecheck.yml's report"]; compared {
		t.Error("compared the report of a codecheck.yml there is none of")
	}
}

// A record on the sandbox is in the sandbox's community, which has an
// identifier of its own.
func TestAuditOfASandboxRecord(t *testing.T) {
	s := newStub(t)
	inSandbox := strings.Replace(invenioJSON, zenodoCommunityID, zenodoSandboxCommunityID, 1)
	s.Handle("/zenodo-sandbox/records/1234567", invenioOnly(inSandbox))
	s.Handle("/zenodo/records/1234567", invenioOnly(inSandbox))
	if got := statuses(s.audit(t, ZenodoTarget{ID: "1234567", Sandbox: true}))["in the CODECHECK community"]; got != StatusPass {
		t.Errorf("on the sandbox: community %s, want pass", got)
	}
	if got := statuses(s.audit(t, ZenodoTarget{ID: "1234567"}))["in the CODECHECK community"]; got != StatusFail {
		t.Errorf("on zenodo.org: community %s, want fail for the sandbox's community", got)
	}
}
