package bot

import (
	"net/http"
	"strings"
	"testing"

	"github.com/codecheckers/chekhov/internal/command"
)

// shareSecret stands for a share link's token in a command.
const shareSecret = "s3cr3t-t0ken"

// auditing is readying with testdata/valid-2.0's record on Zenodo: published,
// at the report DOI, and in the curation policy's good order but for the
// publisher.
func auditing(t *testing.T) *Server {
	t.Helper()
	server, stub := readyingWith(t, demoIssueBody, "https://doi.org/10.5555/preprint.1")
	server.Services.Zenodo = stub.URL + "/zenodo"
	record := `{"id": "1234567", "pids": {"doi": {"identifier": "10.5281/zenodo.1234567"}},
	  "parent": {"communities": {"ids": ["505e49f2-de4f-4905-9666-36c7303e497d"]}},
	  "versions": {"index": 1, "is_latest": true},
	  "files": {"entries": {"codecheck.pdf": {"key": "codecheck.pdf"}}},
	  "metadata": {"title": "CODECHECK Certificate 1970-001", "publisher": "Zenodo"}}`
	stub.Handle("/zenodo/records/1234567", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("token") && r.URL.Query().Get("token") != shareSecret {
			http.Error(w, "invalid token", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(record))
	})
	return server
}

// Without an argument, the record is the report of the check's codecheck.yml,
// and the reply says which codecheck.yml that was.
func TestCheckZenodoAuditsTheReport(t *testing.T) {
	reply := answer(t, auditing(t), command.CheckZenodo, nil, "")
	for _, want := range []string{
		"✖ **The Zenodo record does not follow the curation policy yet**",
		"Audited `10.5281/zenodo.1234567`",
		"the `codecheck.yml` of `github::codecheckers/demo`, named by the issue's Repository line",
		"- ✖ publisher: Zenodo, where the policy asks for `CODECHECK Community on Zenodo`",
		"- ✔ title: CODECHECK Certificate 1970-001",
		"- ✔ the codecheck.yml's report: names this record",
		"**The register rules on the record**",
	} {
		if !strings.Contains(reply, want) {
			t.Errorf("the reply does not say %q:\n%s", want, reply)
		}
	}
	if strings.Contains(reply, "share link") {
		t.Errorf("the reply speaks of a share link nobody gave:\n%s", reply)
	}
}

// A share link is used and never repeated, and the reply asks for the comment
// holding it to be deleted.
func TestCheckZenodoNeverRepeatsTheToken(t *testing.T) {
	server := auditing(t)
	for _, argument := range []string{
		"https://zenodo.org/records/1234567?token=" + shareSecret,
		"https://zenodo.org/records/7654321?token=" + shareSecret,
		"https://zenodo.org/communities/codecheck?token=" + shareSecret,
	} {
		reply := answer(t, server, command.CheckZenodo, []string{argument}, "")
		if strings.Contains(reply, shareSecret) {
			t.Errorf("%s: the reply repeats the token:\n%s", argument, reply)
		}
	}
	reply := answer(t, server, command.CheckZenodo, []string{"https://zenodo.org/records/1234567?token=" + shareSecret}, "")
	if !strings.Contains(reply, "read with its share link") || !strings.Contains(reply, "Delete that comment") {
		t.Errorf("the reply does not say it used the link, or does not ask for it to be deleted:\n%s", reply)
	}
}

// A certificate on OSF or ResearchEquals is out of scope, not a failure.
func TestCheckZenodoOnAnotherPlatform(t *testing.T) {
	for argument, platform := range map[string]string{
		"10.17605/OSF.IO/ABCDE": "OSF",
		"10.53962/abcd-1234":    "ResearchEquals",
	} {
		reply := answer(t, auditing(t), command.CheckZenodo, []string{argument}, "")
		if !strings.Contains(reply, "is on "+platform) || strings.Contains(reply, "✖") {
			t.Errorf("%s: want out of scope on %s:\n%s", argument, platform, reply)
		}
	}
}
