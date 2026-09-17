package command

import (
	_ "embed"
	"fmt"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// quotesFile is the bundled collection of Chekhov quotes the bot answers
// "thanks" with. It is embedded rather than fetched, because a pleasantry must
// not depend on a service being up, and it is edited by hand: see the header of
// the file itself.
//
//go:embed data/quotes.yml
var quotesFile []byte

// A Quote is one thing Chekhov wrote, with enough of its origin to be quoted
// honourably: the work, when it is from, and where in it.
type Quote struct {
	Text   string `yaml:"text"`
	Work   string `yaml:"work"`
	Year   int    `yaml:"year"`
	Detail string `yaml:"detail"`
}

// collection is the file as it is written.
type collection struct {
	Source  string  `yaml:"source"`
	Licence string  `yaml:"licence"`
	Quotes  []Quote `yaml:"quotes"`
}

var quotes = sync.OnceValues(func() (collection, error) {
	var parsed collection
	if err := yaml.Unmarshal(quotesFile, &parsed); err != nil {
		return collection{}, fmt.Errorf("could not read the bundled quotes: %w", err)
	}
	return parsed, nil
})

// Cite is the work a quote is from, as it goes under the quote: the title, the
// year where the page gives one, and the act, chapter or date where it does.
func (q Quote) Cite() string {
	var out strings.Builder
	fmt.Fprintf(&out, "*%s*", q.Work)
	if q.Year != 0 {
		fmt.Fprintf(&out, " (%d)", q.Year)
	}
	if q.Detail != "" {
		fmt.Fprintf(&out, ", %s", q.Detail)
	}
	return out.String()
}
