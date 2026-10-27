package bot

import (
	"context"
	"errors"
	"net/http"
	"slices"
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
		"**The other register rules on the record**",
	} {
		if !strings.Contains(reply, want) {
			t.Errorf("the reply does not say %q:\n%s", want, reply)
		}
	}
	if strings.Contains(reply, "share link") {
		t.Errorf("the reply speaks of a share link nobody gave:\n%s", reply)
	}
}

// A share link is used and never repeated, and the reply says that anyone who
// can read the issue can use it.
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
	if !strings.Contains(reply, "read with its share link") || !strings.Contains(reply, "anyone who can read this issue can use it") {
		t.Errorf("the reply does not say it used the link, or does not ask for it to be deleted:\n%s", reply)
	}
}

// deleting is a reply path that deletes comments, or fails to with err.
type deleting struct {
	Poster
	deleted []int64
	err     error
}

func (d *deleting) DeleteComment(_ context.Context, _ string, comment int64) error {
	if d.err != nil {
		return d.err
	}
	d.deleted = append(d.deleted, comment)
	return nil
}

// The comment with a share link is deleted, not edited, wherever in it the
// link is and whatever else is wrong with the command; where it cannot be
// deleted, the reply says so, and never why in GitHub's words.
func TestCheckZenodoDeletesTheShareLink(t *testing.T) {
	link := "https://zenodo.org/records/1234567?token=" + shareSecret
	ask := func(server *Server, comment int64, body string, args ...string) string {
		return server.answer(context.Background(), mention{
			Repository: server.Settings.TargetRepository(), Issue: 1, Author: "nuest", Comment: comment, Body: body,
		}, command.Command{Name: command.CheckZenodo, Args: args}).body
	}
	server := auditing(t)
	replies := &deleting{Poster: server.Replies}
	server.Replies = replies

	for name, c := range map[string]struct {
		body string
		args []string
	}{
		"the argument":       {"", []string{link}},
		"a refused argument": {"", []string{"https://zenodo.org/records/1234567/files/codecheck.pdf?token=" + shareSecret}},
		"the line below":     {"@chekhovbot check zenodo 10.5281/zenodo.1234567\n" + link, []string{"10.5281/zenodo.1234567"}},
	} {
		replies.deleted = nil
		reply := ask(server, 42, c.body, c.args...)
		if !slices.Equal(replies.deleted, []int64{42}) {
			t.Errorf("%s: deleted %v, want the comment that asked, 42", name, replies.deleted)
		}
		if !strings.Contains(reply, "I deleted the comment that asked") || !strings.Contains(reply, "GitHub had already sent it") ||
			strings.Contains(reply, shareSecret) {
			t.Errorf("%s: the reply does not say it deleted the comment, or repeats the token:\n%s", name, reply)
		}
	}

	replies.deleted, replies.err = nil, errors.New("deleting comment 42 on codecheckers/register: GitHub answered 403")
	reply := ask(server, 42, "", link)
	if !strings.Contains(reply, "I could not delete it") || strings.Contains(reply, "403") {
		t.Errorf("the reply does not say the comment could not be deleted, or quotes GitHub:\n%s", reply)
	}
	replies.err = nil
	if reply := ask(server, 0, "", link); len(replies.deleted) > 0 || !strings.Contains(reply, "the issue's description") {
		t.Errorf("deleted %v for a link in the description:\n%s", replies.deleted, reply)
	}
	if reply := ask(server, 42, "", "10.5281/zenodo.1234567"); len(replies.deleted) > 0 || strings.Contains(reply, "share link") {
		t.Errorf("deleted %v without a share link:\n%s", replies.deleted, reply)
	}
}
