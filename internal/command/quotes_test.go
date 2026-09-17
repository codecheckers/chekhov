package command

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestThanksIsACommand(t *testing.T) {
	for _, written := range []string{"thanks", "thanks!", "Thanks.", "thank you", "thx", "cheers"} {
		parsed, addressed := Parse(Bot + " " + written)
		if !addressed || parsed.Name != Thanks {
			t.Errorf("%q parsed as %q, want thanks", written, parsed.Name)
		}
	}
}

// Manners are not work: the listing says what to do next, and being able to
// thank the bot is not something anyone needs to be told.
func TestThanksIsNotListed(t *testing.T) {
	for _, role := range []Role{RoleAnyone, RoleEditor} {
		if listing := Listing(role); strings.Contains(listing, "thanks") {
			t.Errorf("the listing for %s shows thanks: %s", role, listing)
		}
	}
}

func TestEveryQuoteIsAttributed(t *testing.T) {
	collection, err := quotes()
	if err != nil {
		t.Fatalf("the bundled quotes do not parse: %v", err)
	}
	if len(collection.Quotes) < 100 {
		t.Errorf("%d quotes, want the collection from Wikiquote", len(collection.Quotes))
	}
	if collection.Source == "" || collection.Licence == "" {
		t.Error("the collection says neither where it is from nor under what licence")
	}

	for _, quote := range collection.Quotes {
		switch {
		case strings.TrimSpace(quote.Text) == "":
			t.Error("a quote has no text")
		case quote.Work == "":
			t.Errorf("no work for %q", excerpt(quote.Text))
		case utf8.RuneCountInString(quote.Text) > 300:
			// A reply to "thanks" is read in passing: the narrative passages
			// the page also quotes were left out, and one must not creep back.
			t.Errorf("%q is %d characters, too long for a reply",
				excerpt(quote.Text), utf8.RuneCountInString(quote.Text))
		case quote.Year != 0 && (quote.Year < 1860 || quote.Year > 1921):
			t.Errorf("%q is dated %d, outside Chekhov's life and posthumous publication",
				excerpt(quote.Text), quote.Year)
		}
		// The file was converted from wikitext once; markup left in it would
		// be posted as it stands.
		for _, markup := range []string{"[[", "]]", "''", "{{", "<ref"} {
			if strings.Contains(quote.Text, markup) {
				t.Errorf("%q still carries %q", excerpt(quote.Text), markup)
			}
		}
	}
}

func TestThanksReplyQuotesAndCredits(t *testing.T) {
	collection, err := quotes()
	if err != nil {
		t.Fatalf("the bundled quotes do not parse: %v", err)
	}

	first := collection.Quotes[0]
	reply := ThanksReply(func(int) int { return 0 })
	for _, want := range []string{"> " + first.Text, first.Work, collection.Source, "<sub>"} {
		if !strings.Contains(reply, want) {
			t.Errorf("the reply does not say %q: %s", want, reply)
		}
	}
}

// pick is asked for an index into the collection, and nothing else: a picker
// that ignores the count would quote the same line for ever.
func TestThanksReplyAsksForAnIndex(t *testing.T) {
	asked := 0
	ThanksReply(func(n int) int {
		asked = n
		return n - 1
	})
	collection, _ := quotes()
	if asked != len(collection.Quotes) {
		t.Errorf("pick was offered %d quotes, want %d", asked, len(collection.Quotes))
	}
}

func excerpt(text string) string {
	runes := []rune(text)
	if len(runes) <= 60 {
		return text
	}
	return string(runes[:60]) + "…"
}
