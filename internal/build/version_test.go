package build

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// releaseHeading is a released version in CHANGELOG.md, as Keep a Changelog
// writes one. The date is required because the rest of the file carries it and
// a heading without one is half-written.
var releaseHeading = regexp.MustCompile(`(?m)^## \[(\d+\.\d+\.\d+)\] - \d{4}-\d{2}-\d{2}`)

// unreleased is the Unreleased section and whatever is under it, up to the
// next heading.
var unreleased = regexp.MustCompile(`(?ms)^## \[Unreleased\]\s*\n(.*?)(?:^## |\z)`)

// semver is the shape Version has to have. Not the full grammar - this project
// has no pre-release or build metadata - but enough that "0.2" or "v0.2.0"
// fails here rather than in a reply.
var semver = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// The version and the changelog have to agree.
//
// This is the whole enforcement of the bump: the version is a constant somebody
// edits, and the thing that makes them edit it is that forgetting fails the
// fast suite rather than being noticed in review. See CLAUDE.md -> The version.
func TestVersionMatchesTheChangelog(t *testing.T) {
	if !semver.MatchString(Version) {
		t.Fatalf("Version = %q, want a semantic version like 0.2.0", Version)
	}

	raw, err := os.ReadFile("../../CHANGELOG.md")
	if err != nil {
		t.Fatalf("read the changelog: %v", err)
	}
	found := releaseHeading.FindAllSubmatch(raw, -1)
	if found == nil {
		t.Fatal("CHANGELOG.md has no released version heading to check against")
	}
	headings := make([]string, 0, len(found))
	for _, match := range found {
		headings = append(headings, string(match[1]))
	}

	// Newest first is an ordering this asserts rather than assumes, so that a
	// heading inserted in the wrong place fails for that reason and not with a
	// confusing complaint about the version.
	for i := 1; i < len(headings); i++ {
		if !newerThan(headings[i-1], headings[i]) {
			t.Errorf("CHANGELOG.md lists %s above %s; releases go newest first",
				headings[i], headings[i-1])
		}
	}
	if headings[0] != Version {
		t.Errorf("the newest changelog heading is %q and Version is %q.\n"+
			"Bump the constant in the commit that earns it, beside the entry - "+
			"see CLAUDE.md -> The version.", headings[0], Version)
	}

	// Anything parked under Unreleased is behaviour the running version
	// reports with no entry a reply can be traced to, which is the whole point
	// of bumping per commit.
	if section := unreleased.FindSubmatch(raw); section != nil {
		if entries := strings.TrimSpace(string(section[1])); entries != "" {
			t.Errorf("CHANGELOG.md has entries under Unreleased:\n%s\n"+
				"Put them under the version that ships them and bump the constant - "+
				"see CLAUDE.md -> The version.", entries)
		}
	}
}

// newerThan compares two versions the file carries, which are always three
// numbers.
func newerThan(a, b string) bool {
	one, other := parts(a), parts(b)
	for i := range one {
		if one[i] != other[i] {
			return one[i] > other[i]
		}
	}
	return false
}

func parts(version string) [3]int {
	var numbers [3]int
	for i, piece := range strings.SplitN(version, ".", 3) {
		numbers[i], _ = strconv.Atoi(piece)
	}
	return numbers
}
