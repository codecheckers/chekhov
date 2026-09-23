package github

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestIssueReadsTitleAndBody(t *testing.T) {
	var path string
	client := stub(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"number": 7, "title": "Check of a paper", "body": "The code is at ...",
			"assignees": [{"login": "a-codechecker"}]}`))
	})

	issue, err := client.Issue(context.Background(), client.Repository, 7)
	if err != nil {
		t.Fatal(err)
	}
	if want := "/repos/codecheckers/testing-dev-register/issues/7"; path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	if issue.Title != "Check of a paper" || issue.Body != "The code is at ..." {
		t.Errorf("read %+v", issue)
	}
	if len(issue.Assignees) != 1 || issue.Assignees[0] != "a-codechecker" {
		t.Errorf("assignees %v", issue.Assignees)
	}
}

func TestOpenIssuesLeavesOutPullRequests(t *testing.T) {
	var query string
	client := stub(t, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") != "1" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte(`[
			{"number": 1, "title": "A check", "assignees": [{"login": "a-codechecker"}]},
			{"number": 2, "title": "A pull request", "pull_request": {"url": "..."}}]`))
	})

	issues, err := client.OpenIssues(context.Background(), client.Repository)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].Number != 1 {
		t.Errorf("read %+v, want only the check", issues)
	}
	if !strings.Contains(query, "state=open") {
		t.Errorf("query = %q, want only the open issues asked for", query)
	}
}

// A closed check still holds its certificate identifier, so the certificate
// commands read the closed issues too, and have to be able to tell them apart.
func TestAllIssuesReadsTheClosedOnesToo(t *testing.T) {
	var query string
	client := stub(t, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") != "1" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte(`[
			{"number": 1, "title": "Open | 2026-002", "state": "open"},
			{"number": 2, "title": "Abandoned | 2026-001", "state": "closed", "labels": [{"name": "id assigned"}]}]`))
	})

	issues, err := client.AllIssues(context.Background(), client.Repository)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 2 || issues[0].Closed || !issues[1].Closed {
		t.Errorf("read %+v", issues)
	}
	if !strings.Contains(query, "state=all") {
		t.Errorf("query = %q, want every issue asked for", query)
	}
}

func TestIssuesRefuseAnotherRepository(t *testing.T) {
	client := stub(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("a request was made for %s, which should have been refused first", r.URL.Path)
	})

	if _, err := client.Issue(context.Background(), "codecheckers/register", 1); err == nil {
		t.Error("reading an issue of another repository should be refused")
	}
	if _, err := client.OpenIssues(context.Background(), "codecheckers/register"); err == nil {
		t.Error("reading the issues of another repository should be refused")
	}
	if _, err := client.AllIssues(context.Background(), "codecheckers/register"); err == nil {
		t.Error("reading every issue of another repository should be refused")
	}
}
