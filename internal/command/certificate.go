package command

import (
	"fmt"
	"strings"
	"time"
)

// The replies of `next certificate` and `set certificate`.

// A Held identifier is where one was found: an issue, or register.csv when
// Issue is 0. check.Claim has the same shape; this package does not import
// check, so the bot converts.
type Held struct {
	ID    string
	Issue int
}

func (h Held) String() string { return fmt.Sprintf("`%s`, %s", h.ID, h.Where()) }

// Where is where the identifier was found.
func (h Held) Where() string {
	if h.Issue == 0 {
		return "in `register.csv`"
	}
	return fmt.Sprintf("on #%d", h.Issue)
}

// An Unlabelled issue carries identifiers in its title but not the label
// that would make them count.
type Unlabelled struct {
	Issue int
	IDs   []string
}

// NextCertificateReply says which identifier comes next, and why: the highest
// one claimed so far and where it is, so that an editor can check the answer
// rather than trust it. label is the register's label for a check that holds
// an identifier.
func NextCertificateReply(next string, highest *Held, label string, unlabelled []Unlabelled) string {
	var reply strings.Builder
	fmt.Fprintf(&reply, "The next certificate identifier is `%s`.\n\n", next)
	if highest != nil {
		fmt.Fprintf(&reply, "The highest so far is %s.", highest)
	} else {
		fmt.Fprintf(&reply, "Nothing is claimed in %s yet.", next[:4])
	}
	fmt.Fprintf(&reply, " Read from `register.csv` and the titles of the issues labelled `%s`, "+
		"open and closed.\n", label)
	reply.WriteString(unlabelledNote(label, unlabelled))
	return reply.String()
}

// unlabelledNote lists the open issues whose identifiers were not counted.
// Only a label makes a title a claim: an identifier in a title can be a
// mention of somebody else's certificate.
func unlabelledNote(label string, unlabelled []Unlabelled) string {
	if len(unlabelled) == 0 {
		return ""
	}
	var note strings.Builder
	fmt.Fprintf(&note, "\n**Not counted**, no `%s` label:\n\n", label)
	for _, issue := range unlabelled {
		fmt.Fprintf(&note, "- #%d has %s in its title\n", issue.Issue, codeList(issue.IDs))
	}
	return note.String()
}

// CertificateSet is what `set certificate` did.
type CertificateSet struct {
	ID string
	// Label is the register's label for a check that holds an identifier.
	Label string
	// Unchanged says the check already held the identifier.
	Unchanged bool
	// Previous is the identifier this check held before, if any.
	Previous string
	// Forced is how far the identifier jumps, as JumpSentence says it, when an
	// editor confirmed the jump with `anyway`.
	Forced string
	// Title is the issue's new title, empty when it did not change;
	// TitleFailed says it should have and could not.
	Title       string
	TitleFailed bool
	// Labelled says the label was added; LabelFailed that it could not be.
	Labelled    bool
	LabelFailed bool
}

// CertificateSetReply says what `set certificate` did, and is the record of
// it, as AssignedReply is for a role: the record above shows the identifier
// now, and the thread says how it got there.
func CertificateSetReply(set CertificateSet, by string, when time.Time) string {
	var reply strings.Builder
	if set.Unchanged {
		fmt.Fprintf(&reply, "`%s` is already the certificate identifier of this check.\n", set.ID)
	} else {
		fmt.Fprintf(&reply, "`%s` is now the certificate identifier of this check.\n", set.ID)
	}
	if set.Previous != "" {
		fmt.Fprintf(&reply, "\nReplaces `%s`.\n", set.Previous)
	}
	if set.Forced != "" {
		fmt.Fprintf(&reply, "\nConfirmed with `anyway`: `%s` %s\n", set.ID, set.Forced)
	}
	switch {
	case set.Title != "":
		// In a code span: a title is somebody's free text, and a handle in it
		// would notify.
		fmt.Fprintf(&reply, "\nTitle set: %s\n", Code(set.Title))
	case set.TitleFailed:
		reply.WriteString("\nThe title could not be changed; please add the identifier after a `|`.\n")
	}
	switch {
	case set.Labelled:
		fmt.Fprintf(&reply, "\nLabel `%s` added.\n", set.Label)
	case set.LabelFailed:
		fmt.Fprintf(&reply, "\nThe label `%s` could not be added; please add it.\n", set.Label)
	}
	if !set.Unchanged || set.Title != "" || set.Labelled {
		reply.WriteString(entry("set", by, when))
	}
	return reply.String()
}

// CertificateTakenReply refuses an identifier somebody else holds.
func CertificateTakenReply(held Held, rule string) string {
	return fmt.Sprintf("`%s` is taken: it is %s.\n%s", held.ID, held.Where(), rule)
}

// CertificateJumpReply refuses an identifier too far past the year's previous
// one, and says how to go ahead: a gap can be deliberate, a typo cannot tell
// itself apart from one.
func CertificateJumpReply(id, jump, rule string) string {
	return fmt.Sprintf("`%s` %s\n%s\nIf that is intended: `%s set certificate %s anyway`\n",
		id, jump, rule, Bot, id)
}

// JumpSentence says how far an identifier runs past the year's previous one.
func JumpSentence(gap int, previous *Held) string {
	if previous == nil {
		return fmt.Sprintf("is %d numbers into its year, which has no identifier yet.", gap)
	}
	return fmt.Sprintf("is %d past the year's previous identifier, %s.", gap, previous)
}

// RuleNote names one rule a reply is about, with the symbol of its severity
// and its description: an identifier alone tells a reader nothing.
func RuleNote(symbol, id, description string) string {
	return fmt.Sprintf("\n%s `%s`: %s\n", symbol, id, description)
}

// CertificateLine shows the identifier under the roles table, wherever that
// table is shown: the record comment, the `roles` reply and an adoption.
func CertificateLine(id string) string {
	if id == "" {
		return ""
	}
	return fmt.Sprintf("\n**Certificate**: `%s`\n", id)
}
