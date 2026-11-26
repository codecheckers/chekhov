package command

import (
	"strings"
	"testing"
)

const liveLink = "https://zenodo.org/records/1?token=made-up"

// Whatever else quotes a share link, what the bot posts does not carry its
// token.
func TestDefuseCutsOutShareTokens(t *testing.T) {
	for _, quoted := range []string{
		"`@chekhovbot chek zenodo " + liveLink + "SECRET`",
		"<https://sandbox.zenodo.org/records/1?preview=1&token=SECRET>",
		"[a link](zenodo.org/records/1?token=SECRET&x=1) | cell",
		"https://zenodo.org/records/1?preview=1&amp;token=SECRET",
	} {
		if got := Defuse(quoted); strings.Contains(got, "SECRET") || !strings.Contains(got, "token=…") {
			t.Errorf("%q became %q", quoted, got)
		}
	}
	for _, kept := range []string{"https://zenodo.org/records/1", "`token=`", "https://example.org/?token=abc"} {
		if got := Defuse(kept); got != kept {
			t.Errorf("%q became %q", kept, got)
		}
	}
}

// What is cut out of a reply is what the bot knows a share link by, so every
// form it redacts is one it deletes a comment for.
func TestWhatIsRedactedIsHeldToBeAShareLink(t *testing.T) {
	for _, link := range []string{"zenodo.org/records/1?token=x", "https://zenodo.org/records/1?preview=1&amp;token=x",
		"https://sandbox.zenodo.org/records/1?preview=1&token=x"} {
		if !HoldsShareLink(link) || WithoutShareTokens(link) == link {
			t.Errorf("%q: held %v, redacted to %q", link, HoldsShareLink(link), WithoutShareTokens(link))
		}
	}
}
