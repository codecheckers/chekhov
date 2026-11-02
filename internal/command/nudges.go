package command

import (
	"fmt"
	"strings"
)

// What the nightly sweep says about a check that has stalled
// (codecheckers/chekhov#25): to the assigned codechecker of a check that has
// gone quiet, and to the editors about one that has waited for a codechecker.
// Both mention the people they are for, so that they are notified: that is
// the whole of what a nudge is.

// QuietNudge asks the assigned codechecker where a quiet check stands. last
// says this is the last time the bot asks by itself.
func QuietNudge(codechecker string, days int, last bool) string {
	nudge := fmt.Sprintf("%s, nothing has been said on this check for %s. Where does it stand? "+
		"If something is in the way, say so here, so that the editors can help; "+
		"`%s status` shows what I know of it.\n", notifying([]string{codechecker}), Days(days), Bot)
	if last {
		nudge += "\nThis is the last time I will ask; the editors can see the check has gone quiet.\n"
	}
	return nudge
}

// QueueNudge tells the editors that a check has waited for a codechecker.
// configured says the people mentioned are the deployment's substitutes
// rather than the editors team, which a reader must not be left to assume.
func QueueNudge(editors []string, configured bool, label string, days int) string {
	var out strings.Builder
	fmt.Fprintf(&out, "%s, this check has carried %s for %s and nobody is checking it yet. "+
		"`%s codecheckers suggest` proposes who could.\n",
		notifying(editors), Code(label), Days(days), Bot)
	if configured {
		out.WriteString("\n*This deployment is configured to tell the people above rather than " +
			"the editors team, so the editors have not been notified.*\n")
	}
	return out.String()
}
