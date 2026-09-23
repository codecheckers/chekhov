package check

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// answerAbout is what the description says about one fact, for a test to
// assert on without caring how the table is drawn.
func answerAbout(t *testing.T, description Description, fact string) Answer {
	t.Helper()
	for _, answer := range description.Answers() {
		if answer.Fact == fact {
			return answer
		}
	}
	t.Fatalf("no answer about %q in %v", fact, description.Answers())
	return Answer{}
}

func describe(t *testing.T, stub *stub, target string) Description {
	t.Helper()
	description, err := DescribeRepository(target, false, stub.services)
	if err != nil {
		t.Fatalf("describe %s: %v", target, err)
	}
	return description
}

// The whole of what an editor asks before assigning a codechecker, from one
// repository that answers everything.
func TestDescribeAGitHubRepository(t *testing.T) {
	stub := newStub(t)
	stub.JSON("/github/repos/codecheckers/demo/languages", `{"R": 4000, "Python": 9000}`)
	stub.JSON("/github/repos/codecheckers/demo/contents/", `[
	  {"name": "codecheck.yml", "type": "file", "size": 700},
	  {"name": "LICENSE", "type": "file", "size": 1300},
	  {"name": "codecheck", "type": "dir"}
	]`)
	stub.Status("/raw/codecheckers/demo/HEAD/codecheck.yml", http.StatusOK)
	stub.JSON("/github/repos/codecheckers/demo/contents/codecheck", `[
	  {"name": "codecheck.pdf", "type": "file", "size": 2000}
	]`)

	description := describe(t, stub, "codecheckers/demo")

	if description.URL != "https://github.com/codecheckers/demo" {
		t.Errorf("a reader should be able to open it: %q", description.URL)
	}
	if got := answerAbout(t, description, FactLanguages); got.Says != "Python, R" {
		t.Errorf("languages %q, want the most-used first", got.Says)
	}
	if got := answerAbout(t, description, FactLicence); got.Says != "LICENSE" {
		t.Errorf("licence %q", got.Says)
	}
	if got := answerAbout(t, description, FactConfig); got.Says != "present" {
		t.Errorf("codecheck.yml %q", got.Says)
	}
	// Three files, the directory walked into rather than counted.
	if description.Files != 3 || description.Bytes != 4000 {
		t.Errorf("counted %d files and %d bytes", description.Files, description.Bytes)
	}
	if description.Awkward() {
		t.Error("four kilobytes is not an awkward bundle")
	}
}

// "No licence" is a finding an editor needs early, and is not the same answer
// as "I could not look".
func TestARepositoryThatStatesNoLicenceSaysSo(t *testing.T) {
	stub := newStub(t)
	stub.JSON("/github/repos/codecheckers/bare/languages", `{}`)
	stub.JSON("/github/repos/codecheckers/bare/contents/", `[
	  {"name": "analysis.R", "type": "file", "size": 10}
	]`)
	stub.Status("/raw/codecheckers/bare/HEAD/codecheck.yml", http.StatusNotFound)

	description := describe(t, stub, "codecheckers/bare")

	if got := answerAbout(t, description, FactLicence); !got.Known || got.Says != "none stated" {
		t.Errorf("licence %+v, want a plain answer rather than a skip", got)
	}
	if got := answerAbout(t, description, FactConfig); got.Says != "not there" {
		t.Errorf("codecheck.yml %q", got.Says)
	}
	if got := answerAbout(t, description, FactLanguages); !got.Known {
		t.Errorf("a repository with no code has no languages, which is an answer: %+v", got)
	}
	if !strings.Contains(description.note(), "no `codecheck.yml`") ||
		!strings.Contains(description.note(), "no licence") {
		t.Errorf("the note should say both are missing: %q", description.note())
	}
}

// A service having a bad day is "could not check", never "there is none" -
// the same rule the validation checks follow.
func TestARepositoryThatCannotBeReadIsNotAnEmptyOne(t *testing.T) {
	stub := newStub(t)
	stub.Status("/github/repos/codecheckers/blocked/languages", http.StatusForbidden)
	stub.Status("/github/repos/codecheckers/blocked/contents/", http.StatusTooManyRequests)
	stub.Status("/raw/codecheckers/blocked/HEAD/codecheck.yml", http.StatusTooManyRequests)

	description := describe(t, stub, "codecheckers/blocked")

	for _, fact := range []string{FactLanguages, FactLicence, FactConfig, FactSize} {
		if answer := answerAbout(t, description, fact); answer.Known {
			t.Errorf("%s answered %q from a repository nobody could read", fact, answer.Says)
		}
	}
	if description.Files != 0 {
		t.Errorf("a listing that failed counted %d files", description.Files)
	}
}

// An archive deposit has no language statistics and never will, and states
// its licence in its metadata rather than in a file.
func TestDescribeAZenodoDeposit(t *testing.T) {
	stub := newStub(t)
	stub.JSON("/zenodo/records/3674056", `{"metadata": {"license": {"id": "cc-by-4.0"}},
	  "files": [
	    {"key": "codecheck.yml", "size": 700},
	    {"key": "codecheck/codecheck.pdf", "size": 1048576}
	  ]}`)

	description := describe(t, stub, "zenodo::3674056")

	languages := answerAbout(t, description, FactLanguages)
	if languages.Known || !strings.Contains(languages.Says, "does not report languages") {
		t.Errorf("languages %+v, want the deposit to say it cannot answer", languages)
	}
	if got := answerAbout(t, description, FactLicence); got.Says != "cc-by-4.0" {
		t.Errorf("licence %q, want the record's metadata", got.Says)
	}
	if got := answerAbout(t, description, FactSize); got.Says != "2 files, 1.0 MB" {
		t.Errorf("size %q", got.Says)
	}
}

// GitLab's tree gives names and no sizes. Counting those files as empty would
// report a gigabyte of data as nothing at all.
func TestAListingWithoutSizesSaysSoRatherThanCountingZero(t *testing.T) {
	stub := newStub(t)
	stub.JSON("/gitlab/api/v4/projects/cdchck/demo/languages", `{"Julia": 100.0}`)
	stub.Handle("/gitlab/api/v4/projects/cdchck/demo/repository/tree",
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("page") != "1" {
				fmt.Fprint(w, `[]`)
				return
			}
			fmt.Fprint(w, `[{"name": "codecheck.yml", "type": "blob"},
			  {"name": "data.csv", "type": "blob"}]`)
		})
	stub.Status("/gitlab/cdchck/demo/-/raw/main/codecheck.yml", http.StatusOK)

	description := describe(t, stub, "gitlab::cdchck/demo")

	if description.Files != 2 || description.Unsized != 2 || description.Bytes != 0 {
		t.Errorf("counted %d files, %d of them unsized, %d bytes",
			description.Files, description.Unsized, description.Bytes)
	}
	if got := answerAbout(t, description, FactSize); got.Says != "2 files, the listing gives no sizes" {
		t.Errorf("size %q", got.Says)
	}
}

// A bundle bigger than the walk is worth reporting as a floor: the question
// behind the count is whether this is a lot to ask of somebody, and "more
// than two thousand files" answers it.
func TestAHugeBundleIsCountedUpToTheLimitAndFlagged(t *testing.T) {
	entries := make([]BundleEntry, 0, measureFileLimit+10)
	for i := range cap(entries) {
		entries = append(entries, BundleEntry{Name: fmt.Sprintf("file-%d", i), Size: 1})
	}
	// One directory below the root, so that the walk has somewhere left to go
	// when it stops.
	root := append([]BundleEntry{{Name: "more", IsDir: true}}, entries...)

	description := Description{Unknown: map[string]string{}}
	description.measure(&memo{readDir: func(dir string) ([]BundleEntry, error) {
		if dir == "" {
			return root, nil
		}
		return entries, nil
	}})

	if !description.Partial {
		t.Error("a bundle larger than the limit should say the count is a floor")
	}
	if !description.Awkward() {
		t.Error("a bundle that could not be counted to the end is awkward to check")
	}
	if got := description.size(); !strings.HasPrefix(got, "more than ") {
		t.Errorf("size %q, want it to say the count is a floor", got)
	}
}

// A directory that cannot be listed costs that directory, not the count: what
// was counted is still worth knowing, and the answer says it is a floor.
func TestADirectoryThatWillNotListDoesNotSinkTheCount(t *testing.T) {
	description := Description{Unknown: map[string]string{}}
	description.measure(&memo{readDir: func(dir string) ([]BundleEntry, error) {
		if dir == "" {
			return []BundleEntry{{Name: "kept", Size: 10}, {Name: "deeper", IsDir: true}}, nil
		}
		return nil, fmt.Errorf("503 from the service")
	}})

	if description.Files != 1 || description.Bytes != 10 {
		t.Errorf("counted %d files and %d bytes", description.Files, description.Bytes)
	}
	if !description.Partial {
		t.Error("a directory that could not be read makes the count a floor")
	}
	if _, unknown := description.Unknown[FactSize]; unknown {
		t.Error("what was counted should still be reported")
	}
}

// A directory on disk is the command line's own, and has no language
// statistics behind it.
func TestDescribeADirectoryOnDisk(t *testing.T) {
	description, err := DescribeRepository("../../testdata/valid-2.0", true, nil)
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if got := answerAbout(t, description, FactConfig); got.Says != "present" {
		t.Errorf("codecheck.yml %q", got.Says)
	}
	if got := answerAbout(t, description, FactLanguages); got.Known {
		t.Errorf("a directory has no language statistics: %+v", got)
	}
	if description.Files == 0 || description.Bytes == 0 {
		t.Errorf("counted %d files and %d bytes", description.Files, description.Bytes)
	}
	if description.URL != "" {
		t.Errorf("there is nowhere to link a local directory to: %q", description.URL)
	}
}

// A deployment does not read its own disk: a path in a comment is not a
// target, however much it looks like one.
func TestADeploymentDoesNotDescribeAPath(t *testing.T) {
	if _, err := DescribeRepository("../../testdata/valid-2.0", false, nil); err == nil {
		t.Error("a path should not be read as a repository to describe")
	}
}

// Describing a repository means fetching it, and an offline bot says so
// rather than answering from nothing.
func TestDescribingARepositoryNeedsTheServices(t *testing.T) {
	_, err := DescribeRepository("github::codecheckers/demo", false, nil)
	if err == nil || !strings.Contains(err.Error(), "external services") {
		t.Errorf("error %v, want it to say the bot is offline", err)
	}
}

// The word is not a rule area, and must not be one: a part narrows the
// catalogue, and RunPart would answer with an empty report.
func TestRepositoryIsNotAPartOfTheCatalogue(t *testing.T) {
	if IsPart(AboutRepository) {
		t.Error("repository is a description, not a part of the rule catalogue")
	}
}

// --- counting a bundle in one request, codecheckers/chekhov#46 -------------

// counting wires a stub that answers both ways for the same GitHub
// repository: the contents API a directory at a time, and the git tree API
// whole. Each route counts what was asked of it, so a test can say what the
// description cost as well as what it said.
func counting(t *testing.T, stub *stub) (contents, trees *int) {
	t.Helper()
	contents, trees = new(int), new(int)

	root := `[
	  {"name": "codecheck.yml", "type": "file", "size": 700},
	  {"name": "LICENSE", "type": "file", "size": 1300},
	  {"name": "codecheck", "type": "dir"}
	]`
	stub.Handle("/github/repos/codecheckers/demo/contents/",
		func(w http.ResponseWriter, _ *http.Request) { *contents++; fmt.Fprint(w, root) })
	stub.Handle("/github/repos/codecheckers/demo/contents/codecheck",
		func(w http.ResponseWriter, _ *http.Request) {
			*contents++
			fmt.Fprint(w, `[{"name": "codecheck.pdf", "type": "file", "size": 2000}]`)
		})
	stub.Handle("/github/repos/codecheckers/demo/git/trees/HEAD",
		func(w http.ResponseWriter, _ *http.Request) {
			*trees++
			fmt.Fprint(w, `{"truncated": false, "tree": [
			  {"path": "codecheck.yml", "type": "blob", "size": 700},
			  {"path": "LICENSE", "type": "blob", "size": 1300},
			  {"path": "codecheck", "type": "tree"},
			  {"path": "codecheck/codecheck.pdf", "type": "blob", "size": 2000}
			]}`)
		})
	stub.JSON("/github/repos/codecheckers/demo/languages", `{"R": 900}`)
	stub.Status("/raw/codecheckers/demo/HEAD/codecheck.yml", http.StatusOK)
	return contents, trees
}

// The counts are the bundle's, not the endpoint's: the tree API and the walk
// it replaces have to agree, or `check repository` would answer differently
// depending on which platform the bundle is on.
func TestCountingAGitHubBundleCostsOneRequest(t *testing.T) {
	stub := newStub(t)
	_, trees := counting(t, stub)

	description := describe(t, stub, "codecheckers/demo")

	// The same three files and four kilobytes the directory walk counted.
	if description.Files != 3 || description.Bytes != 4000 || description.Unsized != 0 {
		t.Errorf("counted %d files, %d bytes, %d unsized",
			description.Files, description.Bytes, description.Unsized)
	}
	if description.Partial {
		t.Error("a tree that was not truncated is a whole count")
	}
	if *trees != 1 {
		t.Errorf("asked the tree API %d times, want 1", *trees)
	}
}

// The contents API is the right call for one directory, and the rules that ask
// about one directory still use it. Counting must not have changed that.
func TestCountingDoesNotChangeHowOneDirectoryIsRead(t *testing.T) {
	stub := newStub(t)
	contents, _ := counting(t, stub)

	description := describe(t, stub, "codecheckers/demo")

	if got := answerAbout(t, description, FactLicence); got.Says != "LICENSE" {
		t.Errorf("licence %q, want it read from the root listing", got.Says)
	}
	if *contents == 0 {
		t.Error("the root was never listed, so the licence and the configuration were guessed")
	}
	// The root, once, for the licence and the configuration; the walk that
	// would have listed codecheck/ as well is what the tree API replaced.
	if *contents != 1 {
		t.Errorf("listed %d directories, want the root alone", *contents)
	}
}

// GitHub sets `truncated` when the tree was too large to return whole, which
// is the same statement Partial makes: this count is a floor.
func TestATruncatedTreeIsAPartialCount(t *testing.T) {
	stub := newStub(t)
	counting(t, stub)
	stub.JSON("/github/repos/codecheckers/demo/git/trees/HEAD", `{"truncated": true, "tree": [
	  {"path": "codecheck.yml", "type": "blob", "size": 700}
	]}`)

	description := describe(t, stub, "codecheckers/demo")

	if !description.Partial {
		t.Error("a truncated tree should say the count is a floor")
	}
	if got := description.size(); !strings.HasPrefix(got, "more than ") {
		t.Errorf("size %q, want it to say the count is a floor", got)
	}
}

// A submodule counts as the one entry it is, not as the repository behind it
// and not as a directory - which is what the per-directory listing says too,
// since entriesOf reads anything but a `dir` as a file. A description must not
// depend on which endpoint answered it.
func TestASubmoduleCountsAsOneEntry(t *testing.T) {
	stub := newStub(t)
	counting(t, stub)
	stub.JSON("/github/repos/codecheckers/demo/git/trees/HEAD", `{"truncated": false, "tree": [
	  {"path": "codecheck.yml", "type": "blob", "size": 700},
	  {"path": "vendor", "type": "commit"}
	]}`)

	description := describe(t, stub, "codecheckers/demo")

	// The configuration and the submodule, and only the configuration weighs
	// anything: a submodule is a pointer, and the tree gives it no size.
	if description.Files != 2 || description.Bytes != 700 {
		t.Errorf("counted %d files and %d bytes, want the submodule as one weightless entry",
			description.Files, description.Bytes)
	}
}

// The cheap endpoint is an optimisation. A repository whose git tree cannot be
// read may still list its directories one at a time, and the description is
// the same either way - which is what makes it safe to prefer.
func TestATreeThatCannotBeReadFallsBackToTheWalk(t *testing.T) {
	stub := newStub(t)
	contents, _ := counting(t, stub)
	stub.Status("/github/repos/codecheckers/demo/git/trees/HEAD", http.StatusForbidden)

	description := describe(t, stub, "codecheckers/demo")

	if description.Files != 3 || description.Bytes != 4000 {
		t.Errorf("counted %d files and %d bytes, want what the walk counts",
			description.Files, description.Bytes)
	}
	if *contents < 2 {
		t.Errorf("listed %d directories, want the walk to have read them all", *contents)
	}
}

// One paginated walk rather than one per directory. GitLab still gives no
// sizes, so every file stays unsized: reading an absent size as zero would
// report a gigabyte of data as nothing.
func TestCountingAGitLabBundleIsOneWalk(t *testing.T) {
	stub := newStub(t)
	stub.JSON("/gitlab/api/v4/projects/cdchck/demo/languages", `{"Julia": 100.0}`)
	recursive, perDirectory := 0, 0
	stub.Handle("/gitlab/api/v4/projects/cdchck/demo/repository/tree",
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("recursive") != "true" {
				perDirectory++
				fmt.Fprint(w, `[{"name": "codecheck.yml", "type": "blob"},
				  {"name": "codecheck", "type": "tree"}]`)
				return
			}
			recursive++
			if r.URL.Query().Get("page") != "1" {
				fmt.Fprint(w, `[]`)
				return
			}
			fmt.Fprint(w, `[{"name": "codecheck.yml", "type": "blob"},
			  {"name": "codecheck", "type": "tree"},
			  {"name": "codecheck.pdf", "type": "blob"},
			  {"name": "data.csv", "type": "blob"}]`)
		})
	stub.Status("/gitlab/cdchck/demo/-/raw/main/codecheck.yml", http.StatusOK)

	description := describe(t, stub, "gitlab::cdchck/demo")

	if description.Files != 3 || description.Unsized != 3 || description.Bytes != 0 {
		t.Errorf("counted %d files, %d of them unsized, %d bytes",
			description.Files, description.Unsized, description.Bytes)
	}
	if got := answerAbout(t, description, FactSize); got.Says != "3 files, the listing gives no sizes" {
		t.Errorf("size %q", got.Says)
	}
	if recursive != 1 {
		t.Errorf("asked for the recursive tree %d times, want 1", recursive)
	}
	// The root is still read a directory at a time, for the licence and the
	// configuration; what went away is a listing per directory below it.
	if perDirectory != 1 {
		t.Errorf("listed %d directories one at a time, want the root alone", perDirectory)
	}
}

// A bundle in a sub-directory - the `org/repo|path` shape register.csv uses -
// counts its own tree rather than the whole repository's. Fetching everything
// to discard most of it would be the wrong request, and `truncated` would then
// report this bundle's count as a floor because of files elsewhere.
func TestCountingASubDirectoryAsksForItsOwnTree(t *testing.T) {
	stub := newStub(t)
	whole, subtree := 0, 0
	stub.Handle("/github/repos/codecheckers/demo/git/trees/HEAD",
		func(w http.ResponseWriter, _ *http.Request) {
			whole++
			fmt.Fprint(w, `{"truncated": true, "tree": [
			  {"path": "codecheck/codecheck.pdf", "type": "blob", "size": 2000},
			  {"path": "huge.bin", "type": "blob", "size": 99999}
			]}`)
		})
	stub.Handle("/github/repos/codecheckers/demo/git/trees/HEAD:codecheck",
		func(w http.ResponseWriter, _ *http.Request) {
			subtree++
			// Relative to the bundle, which is what the tree-ish buys.
			fmt.Fprint(w, `{"truncated": false, "tree": [
			  {"path": "codecheck.pdf", "type": "blob", "size": 2000}
			]}`)
		})
	stub.JSON("/github/repos/codecheckers/demo/languages", `{"R": 900}`)
	stub.JSON("/github/repos/codecheckers/demo/contents/codecheck",
		`[{"name": "codecheck.pdf", "type": "file", "size": 2000}]`)
	stub.Status("/raw/codecheckers/demo/HEAD/codecheck/codecheck.yml", http.StatusNotFound)

	description := describe(t, stub, "github::codecheckers/demo|codecheck")

	if subtree != 1 || whole != 0 {
		t.Errorf("asked for the subtree %d times and the whole tree %d, want 1 and 0", subtree, whole)
	}
	if description.Files != 1 || description.Bytes != 2000 {
		t.Errorf("counted %d files and %d bytes, want the sub-directory alone",
			description.Files, description.Bytes)
	}
	// The repository is truncated and this bundle is not: `truncated` on the
	// whole tree says nothing about a complete subtree.
	if description.Partial {
		t.Error("a complete subtree was reported as a floor")
	}
}
