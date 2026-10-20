package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// The pull request adding a row (#21). What matters most is what leaves: the
// requests are recorded, and every test reads them.

// fakeRegister is just enough of a repository to open a pull request against:
// commits with their parents, author and register file, the branches pointing
// at them, and the pull requests. Every position a branch is moved to is kept,
// so a test can say it never pointed at the default branch.
type fakeRegister struct {
	mu       sync.Mutex
	refs     map[string]string // branch -> commit
	commits  map[string]fakeCommit
	trees    map[string]string // tree -> register.csv
	pulls    map[string]int    // head branch -> number
	texts    map[int]string    // number -> title + "\n" + body
	moves    []string          // "branch=commit", every time a ref is written
	requests []sent
}

type fakeCommit struct {
	parent, file, author string
	merges               string // the other parent of a merge commit
}

const registerCSV = "Certificate,Repository,Type,Venue,Issue\n" +
	"1970-001,github::codecheckers/demo,journal,FAKE PeerJ Computer Science,101"

func newFakeRegister() *fakeRegister {
	return &fakeRegister{
		refs:    map[string]string{"main": "base-sha"},
		commits: map[string]fakeCommit{"base-sha": {file: registerCSV, author: "nuest"}},
		trees:   map[string]string{},
		pulls:   map[string]int{},
		texts:   map[int]string{},
	}
}

// resolve is a ref as the contents endpoint reads one: a branch, or a commit.
func (f *fakeRegister) resolve(ref string) string {
	if sha, found := f.refs[ref]; found {
		return sha
	}
	return ref
}

func (f *fakeRegister) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	raw, _ := io.ReadAll(r.Body)
	f.requests = append(f.requests, sent{Method: r.Method, Path: r.URL.Path, Body: string(raw)})
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	w.Header().Set("Content-Type", "application/json")

	path := strings.TrimPrefix(r.URL.Path, "/repos/codecheckers/testing-dev-register")
	switch {
	case path == "" && r.Method == http.MethodGet:
		fmt.Fprint(w, `{"default_branch": "main"}`)
	case strings.HasPrefix(path, "/git/ref/heads/") && r.Method == http.MethodGet:
		sha, found := f.refs[strings.TrimPrefix(path, "/git/ref/heads/")]
		if !found {
			http.Error(w, `{"message": "Not Found"}`, http.StatusNotFound)
			return
		}
		fmt.Fprintf(w, `{"object": {"sha": %q}}`, sha)
	case strings.HasPrefix(path, "/git/commits/") && r.Method == http.MethodGet:
		sha := strings.TrimPrefix(path, "/git/commits/")
		parents := "[]"
		if parent := f.commits[sha].parent; parent != "" {
			parents = fmt.Sprintf(`[{"sha": %q}]`, parent)
		}
		fmt.Fprintf(w, `{"sha": %q, "tree": {"sha": "tree-of-%s"}, "parents": %s}`, sha, sha, parents)
	case path == "/contents/register.csv" && r.Method == http.MethodGet:
		text := f.commits[f.resolve(r.URL.Query().Get("ref"))].file
		fmt.Fprintf(w, `{"content": %q, "encoding": "base64"}`, base64.StdEncoding.EncodeToString([]byte(text)))
	case path == "/git/trees" && r.Method == http.MethodPost:
		tree := fmt.Sprintf("tree-%d", len(f.trees))
		f.trees[tree] = body["tree"].([]any)[0].(map[string]any)["content"].(string)
		fmt.Fprintf(w, `{"sha": %q}`, tree)
	case path == "/git/commits" && r.Method == http.MethodPost:
		sha := fmt.Sprintf("commit-%d", len(f.commits))
		f.commits[sha] = fakeCommit{parent: body["parents"].([]any)[0].(string),
			file: f.trees[body["tree"].(string)], author: "chekhovbot"}
		fmt.Fprintf(w, `{"sha": %q}`, sha)
	case path == "/git/refs" && r.Method == http.MethodPost:
		branch := strings.TrimPrefix(body["ref"].(string), "refs/heads/")
		f.refs[branch] = body["sha"].(string)
		f.moves = append(f.moves, branch+"="+f.refs[branch])
		fmt.Fprint(w, `{}`)
	case strings.HasPrefix(path, "/git/refs/heads/") && r.Method == http.MethodPatch:
		branch := strings.TrimPrefix(path, "/git/refs/heads/")
		f.refs[branch] = body["sha"].(string)
		f.moves = append(f.moves, branch+"="+f.refs[branch])
		fmt.Fprint(w, `{}`)
	case strings.HasPrefix(path, "/compare/") && r.Method == http.MethodGet:
		// The commits on the branch that the default branch does not have.
		onMain := map[string]bool{}
		for sha := f.refs["main"]; sha != ""; sha = f.commits[sha].parent {
			onMain[sha] = true
		}
		var commits []string
		for sha := f.refs[path[strings.LastIndex(path, "...")+3:]]; sha != "" && !onMain[sha]; sha = f.commits[sha].parent {
			parents := `[{}]`
			if f.commits[sha].merges != "" {
				parents = `[{}, {}]`
			}
			commits = append(commits, fmt.Sprintf(`{"author": {"login": %q}, "parents": %s}`, f.commits[sha].author, parents))
		}
		fmt.Fprintf(w, `{"commits": [%s]}`, strings.Join(commits, ","))
	case path == "/pulls" && r.Method == http.MethodGet:
		head := strings.TrimPrefix(r.URL.Query().Get("head"), "codecheckers:")
		if number, found := f.pulls[head]; found && r.URL.Query().Get("base") == "main" {
			title, text, _ := strings.Cut(f.texts[number], "\n")
			fmt.Fprintf(w, `[{"number": %d, "html_url": "https://github.example/pull/%d", "title": %q, "body": %q}]`,
				number, number, title, text)
			return
		}
		fmt.Fprint(w, `[]`)
	case path == "/pulls" && r.Method == http.MethodPost:
		number := 300 + len(f.pulls)
		f.pulls[body["head"].(string)] = number
		f.texts[number] = body["title"].(string) + "\n" + body["body"].(string)
		fmt.Fprintf(w, `{"number": %d, "html_url": "https://github.example/pull/%d"}`, number, number)
	case strings.HasPrefix(path, "/pulls/") && r.Method == http.MethodPatch:
		var number int
		fmt.Sscanf(strings.TrimPrefix(path, "/pulls/"), "%d", &number)
		f.texts[number] = body["title"].(string) + "\n" + body["body"].(string)
		fmt.Fprint(w, `{}`)
	default:
		http.Error(w, `{"message": "not in the fake"}`, http.StatusTeapot)
	}
}

// writes are the requests that changed something, as "METHOD path".
func (f *fakeRegister) writes() []string {
	var writes []string
	for _, request := range f.requests {
		if request.Method != http.MethodGet {
			writes = append(writes, request.Method+" "+
				strings.TrimPrefix(request.Path, "/repos/codecheckers/testing-dev-register"))
		}
	}
	return writes
}

// fileOn is the register file a branch holds.
func (f *fakeRegister) fileOn(branch string) string { return f.commits[f.refs[branch]].file }

func proposing(t *testing.T, fake *fakeRegister) *Client {
	t.Helper()
	client := stub(t, fake.handler)
	client.RegisterFile, client.Account = "register.csv", "chekhovbot"
	return client
}

var proposal = RowProposal{
	Row: map[string]string{"Certificate": "1970-021", "Repository": "github::codecheckers/demo",
		"Type": "journal", "Venue": "FAKE eLife, the testing one", "Issue": "225"},
	Title: "add 1970-021", Body: "closes #225", Message: "add 1970-021, closes #225",
}

const row1970021 = `1970-021,github::codecheckers/demo,journal,"FAKE eLife, the testing one",225` + "\n"

func propose(t *testing.T, client *Client, p RowProposal) (RowPullRequest, error) {
	t.Helper()
	return client.ProposeRow(context.Background(), "codecheckers/testing-dev-register", p)
}

// The first proposal commits the row on top of the default branch, makes the
// branch point at that commit, and opens a pull request - those writes and no
// others, and the default branch untouched.
func TestTheFirstProposalOpensAPullRequest(t *testing.T) {
	fake := newFakeRegister()
	pull, err := propose(t, proposing(t, fake), proposal)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if pull.Number != 300 || pull.Updated {
		t.Errorf("pull request %+v", pull)
	}
	want := []string{"POST /git/trees", "POST /git/commits", "POST /git/refs", "POST /pulls"}
	if got := fake.writes(); strings.Join(got, "; ") != strings.Join(want, "; ") {
		t.Errorf("writes %q, want %q", got, want)
	}
	// The testing register's file ends without a newline; the row is not
	// glued to its last line, and a venue with a comma is quoted.
	if got := fake.fileOn("register-1970-021"); got != registerCSV+"\n"+row1970021 {
		t.Errorf("the branch's file is\n%s", got)
	}
	if fake.commits[fake.refs["register-1970-021"]].parent != "base-sha" {
		t.Errorf("the commit is not on the default branch")
	}
	if fake.refs["main"] != "base-sha" {
		t.Errorf("the default branch moved to %s", fake.refs["main"])
	}
}

// A second proposal, after the register moved on, commits the row on the new
// default branch and moves the branch straight onto it - never onto the
// default branch itself, which would leave the pull request empty for a
// moment and closed by GitHub - and updates the pull request.
func TestASecondProposalUpdatesThePullRequest(t *testing.T) {
	fake := newFakeRegister()
	client := proposing(t, fake)
	if _, err := propose(t, client, proposal); err != nil {
		t.Fatal(err)
	}
	fake.commits["moved-sha"] = fakeCommit{parent: "base-sha", file: registerCSV + "\n1970-020,x,journal,y,120\n", author: "nuest"}
	fake.refs["main"] = "moved-sha"
	fake.requests = nil

	asked := proposal
	asked.Body = "closes #225, asked again"
	pull, err := propose(t, client, asked)
	if err != nil {
		t.Fatalf("propose again: %v", err)
	}
	if !pull.Updated || pull.Number != 300 {
		t.Errorf("pull request %+v", pull)
	}
	want := []string{"POST /git/trees", "POST /git/commits", "PATCH /git/refs/heads/register-1970-021", "PATCH /pulls/300"}
	if got := fake.writes(); strings.Join(got, "; ") != strings.Join(want, "; ") {
		t.Errorf("writes %q, want %q", got, want)
	}
	if got := fake.fileOn("register-1970-021"); got != registerCSV+"\n1970-020,x,journal,y,120\n"+row1970021 {
		t.Errorf("the row is not on the register as it is now:\n%s", got)
	}
	for _, move := range fake.moves {
		if move == "register-1970-021=base-sha" || move == "register-1970-021=moved-sha" {
			t.Errorf("the branch was pointed at the default branch: %s", move)
		}
	}
}

// Asked again with nothing changed, the branch already is the row on the
// default branch and the pull request already says so: nothing is written,
// and the answer says it changed nothing.
func TestAProposalWithNothingNewWritesNothing(t *testing.T) {
	fake := newFakeRegister()
	client := proposing(t, fake)
	if _, err := propose(t, client, proposal); err != nil {
		t.Fatal(err)
	}
	fake.requests = nil

	pull, err := propose(t, client, proposal)
	if err != nil {
		t.Fatalf("propose again: %v", err)
	}
	if writes := fake.writes(); len(writes) != 0 {
		t.Errorf("writes %q, want none", writes)
	}
	if !pull.Unchanged {
		t.Errorf("the answer does not say nothing changed: %+v", pull)
	}
}

// GitHub's "Update branch" merges the default branch into the bot's branch as
// the editor who clicked it. That is nobody's change to the row: the branch is
// still the bot's to rebuild.
func TestAnUpdateBranchMergeIsNotSomebodysWork(t *testing.T) {
	fake := newFakeRegister()
	client := proposing(t, fake)
	if _, err := propose(t, client, proposal); err != nil {
		t.Fatal(err)
	}
	fake.commits["merge-sha"] = fakeCommit{parent: fake.refs["register-1970-021"], file: fake.fileOn("register-1970-021"),
		author: "nuest", merges: "base-sha"}
	fake.refs["register-1970-021"] = "merge-sha"

	if _, err := propose(t, client, proposal); err != nil {
		t.Errorf("a merge from the default branch was taken for somebody's work: %v", err)
	}
}

// A commit by an editor on the bot's branch is their fix, and is refused
// rather than replaced - with nothing written.
func TestABranchWithSomebodyElsesCommitIsRefused(t *testing.T) {
	fake := newFakeRegister()
	fake.commits["editor-sha"] = fakeCommit{parent: "base-sha", file: registerCSV, author: "nuest"}
	fake.refs["register-1970-021"] = "editor-sha"

	_, err := propose(t, proposing(t, fake), proposal)
	if !errors.Is(err, ErrBranchNotOurs) {
		t.Errorf("err = %v, want ErrBranchNotOurs", err)
	}
	if writes := fake.writes(); len(writes) != 0 {
		t.Errorf("a refusal wrote %q", writes)
	}
}

// A certificate the default branch's file already has is refused, before any
// write: it was registered since the bot last read the register.
func TestACertificateAlreadyRegisteredIsRefused(t *testing.T) {
	fake := newFakeRegister()
	taken := proposal
	taken.Row = map[string]string{"Certificate": "1970-001"}

	_, err := propose(t, proposing(t, fake), taken)
	if !errors.Is(err, ErrAlreadyRegistered) {
		t.Errorf("err = %v, want ErrAlreadyRegistered", err)
	}
	if writes := fake.writes(); len(writes) != 0 {
		t.Errorf("a refusal wrote %q", writes)
	}
}

// What the branch is named from, and where it may be written, are checked
// before any request.
func TestAProposalOutsideItsFenceSendsNothing(t *testing.T) {
	for name, change := range map[string]func(*Client, *RowProposal, *string){
		"a certificate that is not one": func(_ *Client, p *RowProposal, _ *string) {
			p.Row = map[string]string{"Certificate": "main"}
		},
		"another repository":          func(_ *Client, _ *RowProposal, r *string) { *r = "codecheckers/register" },
		"no register file configured": func(c *Client, _ *RowProposal, _ *string) { c.RegisterFile = "" },
		"no account of its own":       func(c *Client, _ *RowProposal, _ *string) { c.Account = "" },
	} {
		fake := newFakeRegister()
		client := proposing(t, fake)
		p, repository := proposal, "codecheckers/testing-dev-register"
		change(client, &p, &repository)
		if _, err := client.ProposeRow(context.Background(), repository, p); err == nil {
			t.Errorf("%s: proposed", name)
		}
		if len(fake.requests) != 0 {
			t.Errorf("%s: sent %+v", name, fake.requests)
		}
	}
}

// The row is written in the file's own line endings, and a header that opens
// with a byte-order mark still has a Certificate column.
func TestTheRowFollowsTheFilesOwnShape(t *testing.T) {
	text := "\ufeffCertificate,Venue\r\n1970-001,x\r\n"
	got, err := withRow(text, map[string]string{"Certificate": "1970-002", "Venue": "y"})
	if err != nil {
		t.Fatal(err)
	}
	if got != text+"1970-002,y\r\n" {
		t.Errorf("got %q", got)
	}
	if _, err := withRow(text, map[string]string{"Certificate": "1970-001"}); !errors.Is(err, ErrAlreadyRegistered) {
		t.Errorf("a certificate behind a byte-order mark was not found: %v", err)
	}
}
