package suggest

import (
	"cmp"
	"strings"

	"github.com/codecheckers/chekhov/internal/check"
)

// What the check is about, from whatever the editor had to hand.
//
// By the time somebody is looking for a codechecker the register holds nothing
// about this check yet - that row is written when the check is finished - so
// the evidence has to come from the conversation: the repository, the paper's
// DOI, or the prose of an abstract or a data and software availability
// statement pasted under the command.

// A Hint is one thing an editor named, already read for what kind of thing it
// is. Classified in one place, so that "a DOI" means the same everywhere.
type Hint struct {
	// Kind is "doi", "repository" or "text".
	Kind  string
	Value string
	// From is where the text came from, for the reply to say what it read.
	// Empty means the comment the command was written in; the fallback to the
	// checks issue says so instead, because claiming to have read a comment
	// that said nothing is worse than saying nothing.
	From string
}

// Read classifies one word an editor wrote.
//
// What counts as a repository is check's to say - the same forms a check
// command accepts - so this asks it rather than keeping a second copy of the
// patterns.
func Read(word string) Hint {
	word = strings.TrimSpace(strings.Trim(word, "<>`"))
	switch target, doi := check.TargetIn(word), check.DOIIn(word); {
	case word == "":
		return Hint{Kind: "text"}
	case target != "":
		return Hint{Kind: "repository", Value: target}
	case doi != "":
		return Hint{Kind: "doi", Value: doi}
	default:
		return Hint{Kind: "text", Value: word}
	}
}

// Hints reads the arguments of the command, and the text pasted under it.
//
// The text is one hint rather than one per word: an abstract is prose, and
// matching it happens against the vocabulary, not word by word.
func Hints(args []string, text string) []Hint {
	var hints []Hint
	var words []string
	for _, arg := range args {
		if hint := Read(arg); hint.Kind == "text" {
			words = append(words, hint.Value)
		} else {
			hints = append(hints, hint)
		}
	}
	// Whatever was not a target is prose too, and joins the pasted text: an
	// editor who writes `suggest codecheckers spatial statistics in R` means
	// those words.
	if prose := strings.TrimSpace(strings.Join(words, " ") + "\n" + text); prose != "" {
		hints = append(hints, Hint{Kind: "text", Value: prose})
	}
	return hints
}

// Gather follows every hint and collects what it says about the check.
//
// A hint that cannot be followed is recorded and the rest continue: a Crossref
// outage must not cost the editor the languages of the repository.
func Gather(services *check.Services, vocabulary Vocabulary, hints []Hint) Evidence {
	var evidence Evidence
	for _, hint := range hints {
		switch hint.Kind {
		case "doi":
			gatherDOI(services, &evidence, vocabulary, hint.Value)
		case "repository":
			gatherRepository(services, &evidence, vocabulary, hint.Value)
		case "text":
			evidence.addLanguages(mentioned(hint.Value, vocabulary.Languages)...)
			evidence.addFields(mentioned(hint.Value, vocabulary.Fields)...)
			evidence.note(cmp.Or(hint.From, "the text of the comment"))
			// A repository or a DOI written inside the prose is worth
			// following: an availability statement is usually nothing but.
			for _, found := range targetsIn(hint.Value) {
				switch found.Kind {
				case "doi":
					gatherDOI(services, &evidence, vocabulary, found.Value)
				case "repository":
					gatherRepository(services, &evidence, vocabulary, found.Value)
				}
			}
		}
	}
	return evidence
}

func gatherDOI(services *check.Services, evidence *Evidence, vocabulary Vocabulary, doi string) {
	work, err := services.Work(doi)
	if err != nil {
		evidence.couldNotRead(doi, err)
		return
	}
	evidence.note(services.MetadataSource() + ", for " + doi)
	evidence.Authors = append(evidence.Authors, work.Authors...)

	// What the source says the paper is about is taken as it is written, and
	// not only where the lists happen to use the same words: "melanopsin" is
	// not in anybody's fields today and is exactly the word that would find
	// the one person who could check that paper tomorrow.
	evidence.addFields(work.Subjects...)

	about := strings.Join([]string{work.Title, work.Journal, work.Abstract}, " ")
	evidence.addLanguages(mentioned(about, vocabulary.Languages)...)
	evidence.addFields(mentioned(about, vocabulary.Fields)...)
}

func gatherRepository(services *check.Services, evidence *Evidence, vocabulary Vocabulary, target string) {
	spec, err := check.ResolveTarget(target, services)
	if err != nil {
		evidence.couldNotRead(target, err)
		return
	}
	languages, err := services.Languages(spec)
	if err != nil {
		evidence.couldNotRead(spec.String(), err)
		return
	}
	evidence.note(spec.String())
	evidence.addLanguages(languages...)
}

// targetsIn finds the repositories and DOIs written inside prose.
//
// Only a link, a `type::path` spec or a DOI counts here. A bare `owner/repo`
// is a repository when an editor names it as the target of the command, and
// ordinary punctuation in a sentence when it is not: "and/or", "input/output",
// "km/h" and "R/Bioconductor" all match the shape, and following each one
// would cost an API call and a line of apology in the reply.
func targetsIn(text string) []Hint {
	var found []Hint
	for _, word := range strings.FieldsFunc(text, func(r rune) bool {
		return r == ' ' || r == '\n' || r == '\t' || r == ',' || r == ';' || r == '(' || r == ')'
	}) {
		word = strings.Trim(word, ".<>`\"'")
		if !strings.Contains(word, "://") && !check.IsRepositorySpec(word) &&
			check.DOIIn(word) == "" {
			continue
		}
		if hint := Read(word); hint.Kind != "text" && hint.Value != "" {
			found = append(found, hint)
		}
	}
	return found
}

// mentioned finds the vocabulary terms a piece of prose uses.
//
// Whole words only, so that "R" is the language and not the r in "research",
// and "go" is not every "going". A term of several words - "geospatial data
// analysis" - is looked for as it is written.
func mentioned(text string, vocabulary []string) []string {
	haystack := normaliseWords(text)
	var found []string
	for _, term := range vocabulary {
		// The term goes through the same normalisation as the text, or a term
		// written "MATLAB/Octave" could never be found in a sentence, where
		// the slash has already become a space.
		if needle := normaliseWords(term); needle != " " &&
			strings.Contains(haystack, needle) {
			found = append(found, term)
		}
	}
	return found
}

// normaliseWords is a piece of text reduced to its words, lowercased, with a
// space at each end so that a search for " r " cannot match the r in
// "research" nor "go" every "going".
func normaliseWords(text string) string {
	words := strings.FieldsFunc(strings.ToLower(text), notWord)
	for i, word := range words {
		// A dot or a dash is part of a name - node.js, f#, c++, scikit-learn -
		// but not at the end of a sentence, where it would hide the word.
		words[i] = strings.Trim(word, ".-")
	}
	return " " + strings.Join(words, " ") + " "
}

func notWord(r rune) bool {
	return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
		r == '+' || r == '#' || r == '-' || r == '.')
}
