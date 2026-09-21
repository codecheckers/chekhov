package build

import (
	"regexp"
	"testing"
)

// A commit, or nothing: never a guess.
//
// What a test binary carries depends on the flags it was built with - the
// default writes no VCS stamp, `-buildvcs=true` writes one - so this asserts
// the shape of the answer rather than which of the two it is. The stamped case
// is exercised by building the binary, which is the only way to get a stamp
// and the reason this source is trusted: there is nothing to inject.
func TestRevisionIsACommitOrNothing(t *testing.T) {
	commit, dirty := Revision()
	switch {
	case commit == "":
		if dirty {
			t.Error("no commit, but the tree was reported dirty")
		}
	case !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(commit):
		t.Errorf("revision = %q, want a full commit or nothing at all", commit)
	}
}
