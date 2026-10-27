package bot

import (
	"os"
	"strings"
	"testing"

	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/testserver"
)

// readying wires a server to check #1 with the body given, and
// github::codecheckers/demo holding testdata/valid-2.0's codecheck.yml, a
// README and a LICENSE. The paper is the stub's /paper, which answers, and
// reference is what paper.reference says: {{server}} stands for the stub.
func readying(t *testing.T, body, reference string) *Server {
	t.Helper()
	server, _ := readyingWith(t, body, reference)
	return server
}

// readyingWith is readying, and the stub, for a test that serves more.
func readyingWith(t *testing.T, body, reference string) (*Server, *testserver.Server) {
	t.Helper()
	server, path := checking(t, "")
	path.issues[0].Body = body

	stub := testserver.New(t)
	configuration, err := os.ReadFile("../../testdata/valid-2.0/codecheck.yml")
	if err != nil {
		t.Fatal(err)
	}
	configuration = []byte(strings.Replace(string(configuration), "https://doi.org/10.5555/preprint.1",
		strings.ReplaceAll(reference, "{{server}}", stub.URL), 1))
	stub.Bytes("/raw/codecheckers/demo/HEAD/codecheck.yml", configuration)
	stub.JSON("/github/repos/codecheckers/demo/contents/", `[
	  {"name": "README.md", "type": "file"},
	  {"name": "LICENSE", "type": "file"},
	  {"name": "codecheck.yml", "type": "file"}
	]`)
	stub.Text("/paper", "the paper")
	server.Services = &check.Services{Access: check.Access{
		HTTP:       stub.Client(),
		Register:   server.Settings.TargetRepository(),
		RawContent: stub.URL + "/raw",
		GitHub:     stub.URL + "/github",
	}}
	return server, stub
}

// Nothing named and nothing recorded: the repository is the issue's, read
// off the Repository line the templates have, and the reply says so.
func TestCheckReadyReadsTheRepositoryLine(t *testing.T) {
	server := readying(t, demoIssueBody, "{{server}}/paper")

	reply := answer(t, server, command.CheckReady, nil, "")
	for _, want := range []string{
		"✔ **Ready for a codechecker**",
		"✔ a README: README.md",
		"✔ a codecheck.yml with a manifest",
		"✔ a licence: LICENSE",
		"✔ a paper to check",
		"named by the issue's Repository line",
		"[author guide](https://codecheck.org.uk/guide/community-workflow-author#2-requirements)",
	} {
		if !strings.Contains(reply, want) {
			t.Errorf("the reply does not say %q:\n%s", want, reply)
		}
	}
}

// A paper.reference still written FIXME is no reference: the issue's link is
// the one asked, and the reply says where it came from.
func TestCheckReadyTakesThePaperFromTheIssue(t *testing.T) {
	server := readying(t, "", "https://doi.org/FIXME")
	stub := strings.TrimSuffix(server.Services.RawContent, "/raw")
	server.Replies.(*workflow).issues[0].Body = demoIssueBody + "**Workflow**: the preprint, " + stub + "/paper\n"

	reply := answer(t, server, command.CheckReady, nil, "")
	if !strings.Contains(reply, "✔ a paper to check") || !strings.Contains(reply, "from the issue") {
		t.Errorf("the issue's paper was not asked:\n%s", reply)
	}
}

// With nothing to look at, the reply asks for the repository rather than
// guessing one.
func TestCheckReadyAsksForTheRepository(t *testing.T) {
	server := readying(t, "**Workflow**: a paper about rain\n", "{{server}}/paper")

	reply := answer(t, server, command.CheckReady, nil, "")
	if !strings.Contains(reply, "which repository to look at") ||
		!strings.Contains(reply, "@chekhovbot check ready github::owner/repo") {
		t.Errorf("the reply does not ask for the repository:\n%s", reply)
	}
}

// The paper is the link the template asks for it on; the code, a deposit and
// the template's own instructions are not the paper, and no other line is
// guessed at.
func TestThePaperIsFoundInTheIssue(t *testing.T) {
	cases := map[string]string{
		"**Workflow**: rain, see https://doi.org/10.1234/rain":                                "https://doi.org/10.1234/rain",
		"**Workflow**: [the preprint](https://arxiv.org/abs/2601.00001)":                      "https://arxiv.org/abs/2601.00001",
		"**Workflow**: see https://arxiv.org/abs/2601.00001.":                                 "https://arxiv.org/abs/2601.00001",
		"**Workflow**: 10.1234/rain":                                                          "https://doi.org/10.1234/rain",
		"**Submission**: <https://osf.io/preprints/metaarxiv/abcde>":                          "https://osf.io/preprints/metaarxiv/abcde",
		"**Workflow**: https://github.com/codecheckers/demo and https://doi.org/10.1234/rain": "https://doi.org/10.1234/rain",
		"**Workflow**: data at https://doi.org/10.5281/zenodo.123, paper 10.1234/rain":        "https://doi.org/10.1234/rain",
		"**Workflow**: code and data at 10.53962/abcd-1234":                                   "",
		"**Workflow**: ... <!-- e.g. https://doi.org/10.1234/example -->":                     "",
		"**Repository**: https://doi.org/10.1234/code":                                        "",
		"The paper is 10.1234/rain.":                                                          "",
		"":                                                                                    "",
	}
	for body, want := range cases {
		if got := paperInIssue(body); got != want {
			t.Errorf("%q: got %q, want %q", body, got, want)
		}
	}
}

// A target the command names that cannot be read is said so, rather than
// answered for the issue's repository instead.
func TestCheckReadySaysWhenItCannotReadTheTarget(t *testing.T) {
	server := readying(t, demoIssueBody, "{{server}}/paper")

	reply := answer(t, server, command.CheckReady, []string{"gitlab.com/me/project/sub/dir"}, "")
	if !strings.Contains(reply, "I cannot read `gitlab.com/me/project/sub/dir` as a repository") ||
		strings.Contains(reply, "Ready for a codechecker") {
		t.Errorf("the reply does not refuse the target:\n%s", reply)
	}
}

// An issue that could not be read is not an issue without a Repository line.
func TestCheckReadySaysWhenItCannotReadTheIssue(t *testing.T) {
	if reply := command.NoReadyTarget(false); !strings.Contains(reply, "could not read the issue") ||
		strings.Contains(reply, "Add one to the issue") {
		t.Errorf("the reply blames the issue:\n%s", reply)
	}
}
