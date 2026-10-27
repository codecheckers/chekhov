package command

import (
	"fmt"
	"strings"
)

// curationPolicy is what `check zenodo` holds a record against.
const curationPolicy = "https://zenodo.org/communities/codecheck/curation-policy"

// An Audit is `check zenodo`'s answer, as the reply shows it.
type Audit struct {
	// Record is the record that was read, never with a share link's token.
	Record string
	// Draft says it is unpublished; Shared that it was read with a share
	// link.
	Draft, Shared bool
	// Problem is why the record could not be read.
	Problem string
	// Link is what became of the comment with a share link, if there was one.
	Link ShareLink
	// Configuration is the codecheck.yml the record was compared with, From
	// where it was named, and NoConfiguration why there was none.
	Configuration, From, NoConfiguration string
	// Policy is every item of the curation policy.
	Policy []AuditItem
	// Rules are the register rules that need attention, of those the policy
	// list does not show, and RulesSummary counts those rules, as
	// Report.Summary does.
	Rules        []RuleLine
	RulesSummary string
}

// An AuditItem is one item of the curation policy. Status is "pass", "fail",
// or "skip" for one that could not be checked, as the checks write it.
type AuditItem struct {
	What, Status, Detail string
}

// A RuleLine is one rule that needs attention, as a list of several shows it:
// the identifier and the finding, without the rule's description.
type RuleLine struct {
	Symbol, ID, Name, Finding string
}

// AuditReply is `check zenodo`'s answer: every policy item, because the
// policy is a checklist a codechecker works through in the record's form, and
// of the register rules only the ones that need attention.
func AuditReply(a Audit) string {
	var out strings.Builder
	if a.Problem != "" {
		fmt.Fprintf(&out, "… **I could not audit %s**: %s.\n", Code(a.Record), a.Problem)
		out.WriteString(ShareLinkNote(a.Link))
		return out.String()
	}
	what := "record"
	if a.Draft {
		what = "draft"
	}

	failed, skipped := 0, 0
	var items strings.Builder
	for _, item := range a.Policy {
		symbol := "✔"
		switch item.Status {
		case "pass":
		case "fail":
			symbol = "✖"
			failed++
		default:
			symbol = "…"
			skipped++
		}
		line := fmt.Sprintf("- %s %s", symbol, item.What)
		if item.Detail != "" {
			line += ": " + item.Detail
		}
		items.WriteString(line + "\n")
	}
	switch {
	case failed > 0:
		fmt.Fprintf(&out, "✖ **The Zenodo %s does not follow the curation policy yet**: %d of %d items need attention.\n",
			what, failed, len(a.Policy))
	case skipped > 0:
		fmt.Fprintf(&out, "… **I could not tell everything** about the Zenodo %s: %d of %d items not checked.\n",
			what, skipped, len(a.Policy))
	default:
		fmt.Fprintf(&out, "✔ **The Zenodo %s follows the curation policy**.\n", what)
	}

	fmt.Fprintf(&out, "\nAudited %s", Code(a.Record))
	switch {
	case a.Draft && a.Shared:
		out.WriteString(", an unpublished draft read with its share link,")
	case a.Draft:
		out.WriteString(", an unpublished draft,")
	case a.Shared:
		out.WriteString(", read with its share link,")
	}
	fmt.Fprintf(&out, " against the [curation policy of the CODECHECK community](%s)", curationPolicy)
	if a.Configuration != "" {
		fmt.Fprintf(&out, " and the `codecheck.yml` of %s, named by %s", Code(a.Configuration), a.From)
	}
	out.WriteString(".\n")
	if a.NoConfiguration != "" {
		fmt.Fprintf(&out, "Not compared with a `codecheck.yml`: %s.\n", a.NoConfiguration)
	}
	out.WriteString("\n" + items.String())

	fmt.Fprintf(&out, "\n**The other register rules on the record**: %s.\n", a.RulesSummary)
	if len(a.Rules) > 0 {
		out.WriteString("\n| | Rule | Finding |\n|---|---|---|\n")
		for _, rule := range a.Rules {
			fmt.Fprintf(&out, "| %s | `%s` %s | %s |\n", rule.Symbol, rule.ID, rule.Name,
				escapePipes(rule.Finding))
		}
	}
	if a.Draft {
		out.WriteString("\nWhatever is to be fixed can still be fixed in the draft, before it is published.\n")
	}
	out.WriteString(ShareLinkNote(a.Link))
	return out.String()
}

// A ShareLink is what became of a comment that held a Zenodo share link,
// which anyone who can read the issue can use for as long as it is there.
type ShareLink struct {
	// Held says the command held one; the rest says nothing otherwise.
	Held bool
	// Deleted says the bot deleted the comment, and InDescription that the
	// link is in the issue's description, which it cannot delete.
	Deleted, InDescription bool
}

// ShareLinkNote says what became of the share link, and that neither deleting
// nor editing calls back what GitHub has already sent to whoever watches the
// issue: only deleting the link on Zenodo does.
func ShareLinkNote(l ShareLink) string {
	const revoke = "GitHub had already sent it to everybody watching this issue, so delete the link under " +
		"Share on the record once you are done with it.\n"
	switch {
	case !l.Held:
		return ""
	case l.Deleted:
		return "\nI deleted the comment that asked, because anyone who can read this issue could have used " +
			"the share link in it. " + revoke
	case l.InDescription:
		return "\nThe share link is in the issue's description, where anyone who can read this issue can use it. " +
			"Edit it out. The description keeps its earlier version, and " + revoke
	}
	return "\nThe share link is in the comment that asked, where anyone who can read this issue can use it, " +
		"and I could not delete it. Delete the comment rather than edit it, because an edited comment keeps its " +
		"earlier version. " + revoke
}

// UnreadableRecord is `check zenodo` naming something that is not a Zenodo
// record it can read.
func UnreadableRecord(word, why string) string {
	return fmt.Sprintf("… I cannot read %s as a Zenodo record: %s. Name it by its DOI, its link, "+
		"or a share link:\n\n```\n%s %s 10.5281/zenodo.1234567\n```\n", Code(word), why, Bot, CheckZenodo)
}

// RecordOutOfScope is a certificate deposited somewhere the community's
// curation policy does not apply.
func RecordOutOfScope(record, platform string) string {
	return fmt.Sprintf("… %s is on %s. The curation policy is the one of the CODECHECK community on Zenodo, "+
		"so there is nothing for me to audit; `%s check links` asks whether the deposit answers.\n",
		Code(record), platform, Bot)
}

// NoRecord is `check zenodo` with nothing to audit: no record named, and no
// report in the codecheck.yml of this check.
func NoRecord(why string) string {
	return fmt.Sprintf("… I do not know which record to audit: the command names none, and %s. "+
		"Name it by its DOI, its link, or a share link to the draft:\n\n```\n%s %s 10.5281/zenodo.1234567\n```\n",
		why, Bot, CheckZenodo)
}
