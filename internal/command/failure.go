package command

// Failure is why GitHub did not take a write, as a reply words it.
//
// What a person can do about it is the whole point, and it differs: a rate
// limit passes on its own and asking again in a few minutes is the right
// advice; a refused permission or a token GitHub does not accept never passes,
// and "ask me again" sends them round a loop - which is what
// codecheckers/register#238 was told three times. GitHub answers a permission
// and a secondary rate limit with the same 403, so the reading is
// internal/github's, through the same classifier the retry uses; the words
// are here, once, for every write a reply reports: the register pull request,
// the title, a label, the assignee and a team.
//
// The API's own answer is never quoted: it is the log's, with its URLs and
// whatever a proxy put in the body.
type Failure int

const (
	// FailureOther is a server error, a timeout, anything with no better
	// reading: worth one more try.
	FailureOther Failure = iota
	// FailureRefused is a 403 that is not a rate limit: the bot's account, or
	// its token, may not make this write on this repository.
	FailureRefused
	// FailureUnauthorised is a 401: GitHub did not accept the token at all.
	FailureUnauthorised
	// FailureLimited is a 429, or a 403 its headers or body call a rate limit.
	FailureLimited
)

// Why says what happened and what to do about it, as whole sentences.
func (f Failure) Why() string {
	switch f {
	case FailureRefused:
		return "GitHub refused me permission: my account, or my token, may not do this. " +
			"Asking again will not help until somebody gives me the access it needs."
	case FailureUnauthorised:
		return "GitHub did not accept my token, which has expired or been replaced. " +
			"Asking again will not help until somebody who runs me gives me a new one."
	case FailureLimited:
		return "GitHub asked me to slow down, which is a rate limit and passes on its own. " +
			"Ask me again in a few minutes."
	default:
		return "GitHub failed the request, and what it said is in my log. " +
			"Ask me again; if it fails twice, it needs somebody who can read the log."
	}
}
