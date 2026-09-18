package check

import (
	"fmt"
	"net/url"
	"strings"
)

// What a paper is, according to OpenAlex.
//
// OpenAlex replaced Crossref as the source the bot asks, because Crossref
// stopped answering the question. Ten article DOIs taken from the register:
// Crossref had seven of them (the three arXiv DOIs are DataCite, not
// Crossref), an abstract for four, and an ORCID for seven authors of
// twenty-seven - and its `subject` field, the one that said what a paper was
// about, came back present and empty on every single record. OpenAlex had
// nine of the ten, an abstract for all nine, ORCIDs for twenty authors of
// thirty-two, and a subject for every one.
//
// Crossref is still here, one setting away: see config.Metadata.

// conceptScore is how sure OpenAlex has to be about a concept before it counts
// as what the paper is about. Below it the concepts are the ones every paper
// gets - "computer science" on an acoustics paper - and they say nothing.
const conceptScore = 0.3

type openAlexWork struct {
	DOI         string `json:"doi"`
	DisplayName string `json:"display_name"`
	Title       string `json:"title"`
	// PrimaryLocation carries the journal or repository it appeared in.
	PrimaryLocation struct {
		Source struct {
			DisplayName string `json:"display_name"`
		} `json:"source"`
	} `json:"primary_location"`
	// PrimaryTopic is a four-level hierarchy: topic, subfield, field, domain.
	// The field level - "Neuroscience", "Computer Science" - is the one that
	// speaks the same language as a codechecker's declared fields.
	PrimaryTopic openAlexTopic   `json:"primary_topic"`
	Topics       []openAlexTopic `json:"topics"`
	Keywords     []struct {
		DisplayName string  `json:"display_name"`
		Score       float64 `json:"score"`
	} `json:"keywords"`
	Concepts []struct {
		DisplayName string  `json:"display_name"`
		Score       float64 `json:"score"`
	} `json:"concepts"`
	// AbstractInvertedIndex is the abstract as a word to positions map, which
	// is how OpenAlex avoids redistributing the text itself.
	AbstractInvertedIndex map[string][]int `json:"abstract_inverted_index"`
	Authorships           []struct {
		Author struct {
			DisplayName string `json:"display_name"`
			ORCID       string `json:"orcid"`
		} `json:"author"`
	} `json:"authorships"`
}

type openAlexTopic struct {
	DisplayName string `json:"display_name"`
	Subfield    struct {
		DisplayName string `json:"display_name"`
	} `json:"subfield"`
	Field struct {
		DisplayName string `json:"display_name"`
	} `json:"field"`
	Domain struct {
		DisplayName string `json:"display_name"`
	} `json:"domain"`
}

// openAlexWorkOf asks OpenAlex about one DOI.
//
// The filter endpoint rather than the single-work one: a DOI is a filter, and
// the answer is a list, which is also how a DOI that OpenAlex holds twice - it
// happens - is answered without an error.
func (s *Services) openAlexWorkOf(doi string) (openAlexWork, error) {
	endpoint := s.OpenAlex + "/works?filter=doi:" + url.QueryEscape(doi)
	if s.Mailto != "" {
		// OpenAlex asks callers to say who they are, and answers those who do
		// from a less crowded pool.
		endpoint += "&mailto=" + url.QueryEscape(s.Mailto)
	}

	var answer struct {
		Results []openAlexWork `json:"results"`
	}
	if err := s.getJSON(endpoint, nil, &answer); err != nil {
		// A failure is reported with the URL that failed, and this reply is
		// posted on a public issue: the address a deployment identifies
		// itself with is not for the register to publish.
		return openAlexWork{}, fmt.Errorf("could not ask OpenAlex about %s: %s",
			doi, withoutAddress(err.Error(), endpoint, s.Mailto))
	}
	if len(answer.Results) == 0 {
		return openAlexWork{}, fmt.Errorf("OpenAlex does not know %s", doi)
	}
	return answer.Results[0], nil
}

// withoutAddress takes the query - and with it the mailto - out of a message
// about a URL that failed.
func withoutAddress(message, endpoint, mailto string) string {
	if at := strings.IndexByte(endpoint, '?'); at >= 0 {
		message = strings.ReplaceAll(message, endpoint, endpoint[:at])
	}
	if mailto != "" {
		// Belt and braces: a transport error may quote the URL its own way.
		message = strings.ReplaceAll(message, mailto, "(address)")
		message = strings.ReplaceAll(message, url.QueryEscape(mailto), "(address)")
	}
	return message
}

// work is the described paper, in the terms every caller speaks.
func (w openAlexWork) work() Work {
	described := Work{
		Title:    firstNonEmpty(w.Title, w.DisplayName),
		Journal:  w.PrimaryLocation.Source.DisplayName,
		Abstract: w.abstract(),
		Subjects: w.subjects(),
	}
	for _, authorship := range w.Authorships {
		described.Authors = append(described.Authors, WorkAuthor{
			Name:   strings.TrimSpace(authorship.Author.DisplayName),
			Family: familyNameOf(authorship.Author.DisplayName),
			ORCID:  ORCIDDigits(authorship.Author.ORCID),
		})
	}
	return described
}

// subjects is everything OpenAlex says the paper is about, broadest first:
// the domain and field of its primary topic, then the topics themselves, then
// the keywords, then the concepts it is sure enough about.
//
// Broad and narrow together on purpose. A codechecker writes "neuroscience" in
// their fields, which is a field; another writes "melanopsin", which is a
// keyword; the ranking weighs whichever matched by how rare it is.
func (w openAlexWork) subjects() []string {
	var subjects []string
	add := func(terms ...string) {
		for _, term := range terms {
			if term = strings.TrimSpace(term); term != "" && !ContainsFold(subjects, term) {
				subjects = append(subjects, term)
			}
		}
	}

	// The domain first, and only here: it is the broadest word for the paper,
	// and the loop below never reaches it.
	add(w.PrimaryTopic.Domain.DisplayName)
	for _, topic := range append([]openAlexTopic{w.PrimaryTopic}, w.Topics...) {
		add(topic.DisplayName, topic.Field.DisplayName, topic.Subfield.DisplayName)
	}
	for _, keyword := range w.Keywords {
		add(keyword.DisplayName)
	}
	for _, concept := range w.Concepts {
		if concept.Score >= conceptScore {
			add(concept.DisplayName)
		}
	}
	return subjects
}

// abstract rebuilds the text from the inverted index OpenAlex publishes.
func (w openAlexWork) abstract() string {
	if len(w.AbstractInvertedIndex) == 0 {
		return ""
	}
	last := 0
	for _, positions := range w.AbstractInvertedIndex {
		for _, at := range positions {
			if at > last {
				last = at
			}
		}
	}
	words := make([]string, last+1)
	for word, positions := range w.AbstractInvertedIndex {
		for _, at := range positions {
			if at >= 0 && at < len(words) {
				words[at] = word
			}
		}
	}
	return strings.TrimSpace(strings.Join(words, " "))
}

// familyNameOf is the surname of a display name, which is the last word of it.
//
// OpenAlex gives one name where Crossref gives given and family separately, so
// the surname has to be guessed. The last word is right for the great majority
// and wrong for some; CC-MET-007 only ever uses it to look for a name inside
// another, so a wrong guess costs a finding to check rather than a wrong
// verdict - and the rule reports the whole name it could not find.
func familyNameOf(name string) string {
	fields := strings.Fields(strings.TrimSpace(name))
	if len(fields) == 0 {
		return ""
	}
	return fields[len(fields)-1]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// ContainsFold reports whether a list already holds a term, however it is
// spelled: "Python" and "python" are one language written by two people.
func ContainsFold(list []string, want string) bool {
	for _, item := range list {
		if strings.EqualFold(item, want) {
			return true
		}
	}
	return false
}
