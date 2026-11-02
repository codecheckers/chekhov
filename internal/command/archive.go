package command

import (
	"fmt"
	"strings"
)

// What the bot says about archiving the copy of a checked repository
// (codecheckers/chekhov#27): the warning in `check finish`, the reminder the
// nightly sweep posts once the certificate is registered, and the line that
// ends it.

// Instructions for archiving, by platform: what a reply links so that the
// person reminded does not have to look it up.
const (
	gitHubArchiving = "https://docs.github.com/en/repositories/archiving-a-github-repository/archiving-repositories"
	gitLabArchiving = "https://docs.gitlab.com/user/project/working_with_projects/#archive-a-project"
)

// archiving is the instructions for the platform a repository is on.
func archiving(platform string) string {
	if platform == "gitlab" {
		return gitLabArchiving
	}
	return gitHubArchiving
}

// An Archive is a copy of a checked repository that is not archived yet, as
// the replies about it need it.
type Archive struct {
	// Repository is the copy as register.csv writes it, and Platform its
	// type: "github" or "gitlab".
	Repository, Platform string
	// Editor is whom the reminders go to: the handling editor, or whoever
	// finished the check when there is none. Empty when nobody is known.
	Editor string
	// Most is how many reminders the editor will get at most.
	Most int
	// Unknown says the archived status could not be read at all - a GitLab
	// project, without a token - rather than read as open.
	Unknown bool
}

// ArchiveWarning is what `check finish` says about the copy: archive it once
// the certificate is in the register, and who will be reminded until it is.
func ArchiveWarning(a Archive) string {
	if a.Unknown {
		return fmt.Sprintf("─ I cannot see whether %s is archived: GitLab does not say without a token "+
			"(codecheckers/chekhov#65). Once the certificate is in the register, [archive it](%s) "+
			"so that it no longer changes under the certificate.\n",
			Code(a.Repository), archiving(a.Platform))
	}
	var out strings.Builder
	fmt.Fprintf(&out, "⚠ %s is not archived. Once the certificate is in the register, "+
		"[archive it](%s) so that it no longer changes under the certificate.",
		Code(a.Repository), archiving(a.Platform))
	if a.Editor != "" {
		fmt.Fprintf(&out, " I will remind %s when the register pull request is merged, "+
			"and every two weeks after that until it is, %d times at most.", mention(a.Editor), a.Most)
	}
	out.WriteString("\n")
	return out.String()
}

// ArchiveReminder asks the editor, so that they are notified, to archive the
// copy now that the certificate is registered. last says this is the final
// reminder the bot will send by itself.
func ArchiveReminder(a Archive, certificate string, last bool) string {
	var out strings.Builder
	if a.Editor != "" {
		out.WriteString(notifying([]string{a.Editor}) + ", ")
	}
	fmt.Fprintf(&out, "certificate %s is in the register, and %s is still not archived. "+
		"[Archiving it](%s) keeps it readable, and stops it changing under the certificate.\n",
		Code(certificate), Code(a.Repository), archiving(a.Platform))
	if last {
		out.WriteString("\nThis is the last time I will ask.\n")
	}
	return out.String()
}

// ArchiveGone ends the reminders about a copy that can no longer be read:
// deleted, or made private, so that there is nothing left to archive.
func ArchiveGone(repository string) string {
	return fmt.Sprintf("─ I can no longer read %s, so it has been deleted or made private; "+
		"I will not remind anybody about archiving it again.\n", Code(repository))
}

// ArchiveDone ends the reminders: the copy has been archived. Without a
// mention, because there is nothing left for anybody to do.
func ArchiveDone(repository string) string {
	return fmt.Sprintf("✔ %s is archived now. Thank you; that was the last step of this check.\n",
		Code(repository))
}
