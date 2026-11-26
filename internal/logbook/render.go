package logbook

import (
	"regexp"
	"strings"
	"time"
)

// Redact takes out of a value what must not be copied anywhere, however
// private the place: a credential is rotated, not trusted to an access list.
//
// The records are the bot's own words, but their values are not: an error
// carries what a service answered, a URL what somebody wrote, and a request
// that failed may quote its own header. So every value goes through this,
// and the patterns are the shapes a secret takes rather than the places one
// has been seen.
func Redact(value string) string {
	for _, rule := range redactions {
		value = rule.pattern.ReplaceAllString(value, rule.with)
	}
	return value
}

var redactions = []struct {
	pattern *regexp.Regexp
	with    string
}{
	// GitHub's tokens, classic and fine-grained, by their published prefixes.
	{regexp.MustCompile(`\b(?:gh[opusr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})`), "[token]"},
	// An Authorization header, quoted back by something that failed.
	{regexp.MustCompile(`(?i)\b(bearer|token|basic)\s+[A-Za-z0-9._~+/=-]{12,}`), "$1 [redacted]"},
	// key=value and key: value, for the names a secret goes by.
	{regexp.MustCompile(`(?i)\b([a-z_]*(?:` + secretNames + `)[a-z_]*)(\s*[=:]\s*)("[^"]*"|\S+)`), "$1$2[redacted]"},
	// The query and fragment of a URL: a Zenodo share link's secret is its
	// token parameter, and no query the bot logs is worth what one would cost.
	{regexp.MustCompile(`(https?://[^\s?#"'<>]*)[?#][^\s"'<>)]*`), "$1?…"},
	// Credentials written into a URL.
	{regexp.MustCompile(`(https?://)[^\s/@"'<>]+@`), "$1…@"},
	// A long run of key-like characters that none of the above named: a key
	// in some format nobody here thought of. Commit hashes are 40 and stay.
	{regexp.MustCompile(`\b[A-Za-z0-9+_-]{41,}={0,2}`), "[long value]"},
}

// secretNames are the words a secret goes by, in a key=value pair or as the
// key of an attribute.
const secretNames = `token|secret|password|passwd|api_?key|private_?key`

// hidden are attribute keys whose value is never shown, whatever it holds.
var hidden = regexp.MustCompile(`(?i)(` + secretNames + `|authorization|cookie|signature)`)

// Render writes records as a markdown code block, indented, so that nothing
// in them - a handle, a heading, a fence - does anything where it is posted:
// an @ in an indented block notifies nobody. Every value is redacted, and a
// key that names a secret is shown without its value. With traces false, a
// value holding a stack trace is left out; it is kept for the reports of a
// fatal record, where it is the finding.
func Render(records []Record, traces bool) string {
	var out strings.Builder
	for _, record := range records {
		out.WriteString("    ")
		out.WriteString(record.Time.UTC().Format(time.RFC3339))
		out.WriteString(" ")
		out.WriteString(record.Level.String())
		out.WriteString(" ")
		out.WriteString(OneLine(Redact(record.Message)))
		var multi []Attr
		for _, attr := range record.Attrs {
			switch {
			case hidden.MatchString(attr.Key):
				out.WriteString(" " + attr.Key + "=[hidden]")
			case attr.Key == "stack":
				if traces {
					multi = append(multi, attr)
				}
			case strings.ContainsAny(attr.Value, "\r\n"):
				multi = append(multi, attr)
			default:
				out.WriteString(" " + attr.Key + "=" + quoted(Redact(attr.Value)))
			}
		}
		out.WriteString("\n")
		for _, attr := range multi {
			out.WriteString("      " + attr.Key + ":\n")
			// Every way a line can end, a lone carriage return too: markdown
			// ends a line at one, and a line left unindented would be out of
			// the block, where a handle notifies.
			for _, line := range lineEnds.Split(strings.TrimRight(Redact(attr.Value), "\r\n"), -1) {
				out.WriteString("        " + line + "\n")
			}
		}
	}
	return out.String()
}

// lineEnds are the ways a line ends.
var lineEnds = regexp.MustCompile(`\r\n|\r|\n`)

// OneLine keeps a text on one line, its runs of white space one space each.
func OneLine(text string) string { return strings.Join(strings.Fields(text), " ") }

// quoted is a value as slog's text handler writes one: bare when it is one
// word, quoted when it has spaces.
func quoted(value string) string {
	value = OneLine(value)
	if value == "" || strings.ContainsAny(value, " =\"") {
		return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
	}
	return value
}
