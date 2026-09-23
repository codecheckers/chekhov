package command

import (
	"fmt"
	"strings"
)

// Which of the register's labels follow from what the bot can see on a check.
//
// The labels are how a check is found: `needs codechecker` is the queue,
// `institution` says an arrangement covers this check. They are maintained by
// hand, and nothing notices when they stop describing the issue they are on.
//
// There is no `labels` command, deliberately. A command that has to be
// remembered is a command that is not run, and the checks whose labels are
// wrong are exactly the ones nobody is looking at - so the commands that
// already run at the moments a label should change carry the observation
// instead, and say it in the same comment as their own answer. Which commands
// those are is the `AboutTheCheck` field of their entry in the registry.
//
// One table, here, so that no command carries its own idea of which label
// follows from what. Three things it does not do, and each of them matters:
//
//   - It never refuses. The bot does not know why an editor did what they did;
//     an institutional codechecker may well take an ordinary check. What must
//     not happen is that nobody notices. See codecheckers/chekhov#40.
//   - It never changes a label. A label nobody can justify from the issue is
//     still one somebody may have put there for a reason. A command that
//     changes one does it as its own act, and only for a label the settings
//     list under labels.managed - `set certificate` adding `id assigned`.
//   - Agreement is silence. A reply that ends "and the labels are fine" on
//     every command is noise, and noise is how the useful line gets skipped.

// LabelNames are the register's own label names, which the settings carry
// because they belong to the register rather than to this bot. A name left
// empty switches off whatever would have been said about that label.
type LabelNames struct {
	// Institution marks a check an institution's arrangement covers.
	Institution string
	// NeedsCodechecker is the queue: a check nobody is doing yet.
	NeedsCodechecker string
}

// CheckState is what the bot could see about one check when a command ran.
//
// Every fact here has a companion saying whether it could be read at all,
// because "could not check" must never read as "this is wrong": an unreadable
// issue or an untrustworthy roles record leaves a rule with nothing to say,
// which is not the same as a rule that looked and found agreement.
type CheckState struct {
	Names LabelNames

	// Labels are the labels on the issue, and LabelsRead says whether they
	// were read - an empty slice on its own means the check carries none,
	// which is a different thing.
	Labels     []string
	LabelsRead bool
	// Closed says the check is over, which silences every rule about what
	// still has to happen on it.
	Closed bool

	// AssignedCodechecker is the handle of the codechecker doing this check,
	// empty when nobody is, and RolesRead says whether the record could be
	// read and trusted.
	AssignedCodechecker string
	RolesRead           bool

	// Institution is what the codechecker lists say the assigned codechecker
	// checks for, empty when no row of theirs names one - which is also what
	// a list that could not be read says. AlsoVolunteers says another row of
	// theirs names none, so they are on the ordinary list as well.
	Institution    string
	AlsoVolunteers bool
}

// labelRules is the table: one function per way the labels can stop describing
// the check, asked in the order a reader should meet them.
var labelRules = []func(CheckState) (string, bool){
	institutionalCodechecker,
	queuedWithACodechecker,
	unqueuedWithout,
}

// Disagreements is every way the labels do not describe what the bot can see,
// each as the sentence a reply shows. Empty when they agree, and empty when
// there was nothing to compare against.
func (s CheckState) Disagreements() []string {
	var found []string
	for _, rule := range labelRules {
		if says, ok := rule(s); ok {
			found = append(found, says)
		}
	}
	return found
}

// HasLabel reports whether a check carries a label.
//
// One predicate, because two things ask it about the same check: which team
// an assignment leads to, and whether the labels still describe the check. A
// name matched one way here and another way there would send somebody to the
// institutional team and then warn that the check is not institutional.
//
// Trimmed and compared without case, as GitHub shows label names. An empty
// name is never carried, so a label the settings do not name switches off
// whatever asked about it rather than matching every check that has none.
func HasLabel(labels []string, name string) bool {
	if name = strings.TrimSpace(name); name == "" {
		return false
	}
	for _, carried := range labels {
		if strings.EqualFold(strings.TrimSpace(carried), name) {
			return true
		}
	}
	return false
}

// has reports whether the check carries a label, and never when the labels
// were not read: "could not look" is not "does not carry it".
func (s CheckState) has(label string) bool {
	return s.LabelsRead && HasLabel(s.Labels, label)
}

// named reports whether the settings name a label at all, which is what makes
// a rule about its absence worth asking.
func (s CheckState) named(label string) bool {
	return s.LabelsRead && strings.TrimSpace(label) != ""
}

// Watching reports whether any rule here could have something to say: no
// label named is no rule, and the caller is spared reading the world for an
// answer that cannot come.
func (n LabelNames) Watching() bool {
	return strings.TrimSpace(n.Institution) != "" || strings.TrimSpace(n.NeedsCodechecker) != ""
}

// WantsInstitution reports whether the one rule that needs the codechecker
// lists could still fire, which is what makes reading them worth a request: a
// check already labelled institutional has nothing to be warned about.
func (s CheckState) WantsInstitution() bool {
	return s.named(s.Names.Institution) && !s.has(s.Names.Institution)
}

// codechecker is the assigned codechecker, empty when nobody is or when the
// record could not be trusted.
func (s CheckState) codechecker() string {
	if !s.RolesRead {
		return ""
	}
	return s.AssignedCodechecker
}

// institutionalCodechecker is codecheckers/chekhov#40: an institutional
// codechecker on a check nothing says is institutional.
//
// They check *for their institution*, under an arrangement that institution
// has made. The check they are being given is not the work their institution
// signed up for, and nobody finds out until the certificate is being written.
func institutionalCodechecker(s CheckState) (string, bool) {
	who := s.codechecker()
	if who == "" || s.Institution == "" || !s.WantsInstitution() {
		return "", false
	}
	says := fmt.Sprintf(
		"%s checks for %s, and this check does not carry the `%s` label. "+
			"Is the label missing, or the assignment wrong?",
		mention(who), s.Institution, s.Names.Institution)
	if s.AlsoVolunteers {
		// Being on the ordinary list as well does not settle it: somebody who
		// checks for an institution is institutional for this purpose, and an
		// editor reading the warning should know that is why it was said.
		says += " They are on the ordinary list too, which does not settle it: " +
			"anybody a list says checks for an institution counts as institutional here."
	}
	return says, true
}

// queuedWithACodechecker is a check somebody is doing that is still advertised
// as needing somebody.
func queuedWithACodechecker(s CheckState) (string, bool) {
	who := s.codechecker()
	if who == "" || !s.has(s.Names.NeedsCodechecker) {
		return "", false
	}
	return fmt.Sprintf("%s is the assigned codechecker, and this check still carries the `%s` label.",
		mention(who), s.Names.NeedsCodechecker), true
}

// unqueuedWithout is the opposite, and the one nobody notices: a check nobody
// is doing that is invisible to the queue it should be in.
//
// The only rule here that fires on a check where nothing has happened yet, so
// it is also the only one that can speak up where there is nothing to speak
// about. A closed issue silences it: the queue is for work that is still
// wanted, and a finished check whose codechecker was never recorded by this
// bot would otherwise be told off every time somebody ran `roles` on it.
//
// What is left is an open issue of the register that is not a check at all -
// a discussion, or the admin issue. Somebody has to run a command about the
// state of a check on it to see the line, which is itself a mistake, and the
// answer is the same either way: the bot has no way to tell a check from any
// other issue, and inventing one here would be a second idea of what a check
// is, kept beside the register's own.
func unqueuedWithout(s CheckState) (string, bool) {
	if s.Closed || !s.RolesRead || s.AssignedCodechecker != "" ||
		!s.named(s.Names.NeedsCodechecker) || s.has(s.Names.NeedsCodechecker) {
		return "", false
	}
	return fmt.Sprintf("Nobody is the assigned codechecker, and this check does not carry the `%s` "+
		"label, so nobody looking for work will find it.", s.Names.NeedsCodechecker), true
}

// Note is what a reply says about this check's labels, empty when there is
// nothing to say. One door, so that a caller cannot half-perform it.
func (s CheckState) Note() string { return LabelNote(s.Disagreements()) }

// LabelNote is what a reply says about labels that no longer describe the
// check, empty when there is nothing to say.
//
// One note, however many rules had something to say, and never more than one
// per reply: it is appended centrally, after the command has answered, so a
// command cannot say it twice and no command has to remember to say it at all.
func LabelNote(disagreements []string) string {
	if len(disagreements) == 0 {
		return ""
	}
	var note strings.Builder
	note.WriteString("⚠ **The labels may not describe this check.**\n\n")
	for _, says := range disagreements {
		fmt.Fprintf(&note, "- %s\n", says)
	}
	note.WriteString("\nI have changed no labels.\n")
	return note.String()
}
