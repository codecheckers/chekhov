package command

import (
	"fmt"
	"strings"
)

// Deployment is what a reply says about the bot answering it.
//
// The register is in there deliberately: a development bot and the real one
// look alike in a comment, and a codechecker has to be able to tell which one
// just answered them.
//
// How much else a reply says depends on the Environment. A development
// deployment is being watched by whoever is developing it, and every detail -
// the build, the commit, the register - saves a question. A production
// deployment is read by codecheckers who want an answer, not a fingerprint of
// the machine that gave it.
type Deployment struct {
	Version     string
	Commit      string
	Register    string
	Bot         string
	Environment string
}

// Development reports whether this deployment may talk about itself.
func (d Deployment) Development() bool {
	return d.Environment == "" || d.Environment == "development"
}

// Signature goes under every comment in development: which bot, which build,
// which register. In production a reply is signed by the account that posts
// it, which is all a reader needs.
func (d Deployment) Signature() string {
	if !d.Development() {
		return ""
	}
	return fmt.Sprintf("`@%s` %s · working on `%s`", d.Bot, d.describeVersion(), d.Register)
}

// Listing is the reply to "@chekhovbot commands", generated from the registry
// and filtered by what the asker may run.
func Listing(role Role) string {
	var out strings.Builder
	out.WriteString("**Commands I understand**\n")

	visible := Visible(role)
	for _, group := range groups() {
		inGroup := make([]Definition, 0, len(visible))
		for _, definition := range visible {
			if definition.Group == group {
				inGroup = append(inGroup, definition)
			}
		}
		if len(inGroup) == 0 {
			continue
		}

		fmt.Fprintf(&out, "\n**%s**\n\n| Command | What it does |\n|---|---|\n", group)
		for _, definition := range inGroup {
			fmt.Fprintf(&out, "| `%s %s` | %s |\n",
				Bot, escapePipes(definition.Usage), escapePipes(definition.Summary))
		}
	}

	out.WriteString("\nWrite the command on the **first line** of a comment, one command per comment.\n")
	return out.String()
}

// HelloReply is the answer to the liveness check: short, and specific enough
// that it says which deployment is awake. Which register it works on is said
// either way - that is the point of the command - and the build only where
// someone is watching the build.
func (d Deployment) HelloReply() string {
	if !d.Development() {
		return fmt.Sprintf("Hello! I am awake and working on `%s`.\n\n"+
			"Type `%s commands` to see what I can do.\n", d.Register, Bot)
	}
	return fmt.Sprintf("Hello! I am awake, running %s and working on `%s`.\n\n"+
		"Type `%s commands` to see what I can do.\n",
		d.describeVersion(), d.Register, Bot)
}

// UnknownReply answers a comment addressed to the bot that it cannot read,
// quoting back what it saw so the writer can tell a typo from a
// misunderstanding.
func UnknownReply(parsed Command) string {
	var out strings.Builder
	if parsed.Raw == "" {
		out.WriteString("You called, but did not say what for.\n")
	} else {
		fmt.Fprintf(&out, "I do not know the command `%s`.\n", parsed.Raw)
	}

	if suggestion, found := Suggest(firstWord(parsed.Raw)); found {
		fmt.Fprintf(&out, "\nDid you mean `%s %s`?\n", Bot, suggestion)
	}
	fmt.Fprintf(&out, "\nType `%s commands` for the list.\n", Bot)
	return out.String()
}

// describeVersion names the build, with the commit when there is one.
func (d Deployment) describeVersion() string {
	if d.Commit == "" || !d.Development() {
		return "version " + d.Version
	}
	return fmt.Sprintf("version %s (`%s`)", d.Version, short(d.Commit))
}

func short(commit string) string {
	if len(commit) > 8 {
		return commit[:8]
	}
	return commit
}

// escapePipes keeps a usage line like "check [config|bundle]" from breaking
// the table it sits in. A code span does not protect a pipe in GitHub's
// Markdown tables.
func escapePipes(text string) string { return strings.ReplaceAll(text, "|", "\\|") }

func firstWord(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}
