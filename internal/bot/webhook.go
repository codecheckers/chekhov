// Package bot is the listener: it receives GitHub's events, decides whether
// they are genuine and meant for it, and hands the comment to the command
// parser.
package bot

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/codecheckers/chekhov/internal/github"
	"github.com/codecheckers/chekhov/internal/people"
)

// event is the part of GitHub's issues and issue_comment payloads the bot
// reads. Both carry a repository, an issue and an actor; only the comment body
// differs, and an opened issue is treated as a comment made of its own body.
type event struct {
	Action     string `json:"action"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Issue struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		User   struct {
			Login string `json:"login"`
		} `json:"user"`
	} `json:"issue"`
	Comment struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
	} `json:"comment"`
}

// mention is what the bot acts on: one comment, on one issue, by one person.
type mention struct {
	Repository string
	Issue      int
	Author     string
	Body       string

	// read is the issue this mention is on, read once for the whole command.
	// Three parts of one command want it - which team an assignment leads to,
	// what the check is about, and whether the labels still describe it - and
	// nothing the bot does changes a label or a title, so one read serves all
	// three. Set up by answer and shared by every copy of the mention made
	// after that; nil means "read it yourself", which is what a caller
	// outside the dispatch gets. See Server.issue.
	read func() (github.Issue, error)
}

// check names the issue, the one way it is written everywhere: in a signed
// record, in a problem the sweep reports, and in a log line.
func (m mention) check() string { return people.CheckOf(m.Repository, m.Issue) }

// parseEvent reads a delivery, and says whether it is one the bot acts on.
//
// Only an issue being opened and a comment being created are: an edited
// comment would make the bot answer the same instruction twice, and every
// other event type is somebody else's business.
func parseEvent(eventType string, payload []byte) (mention, bool, error) {
	var parsed event
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return mention{}, false, fmt.Errorf("could not read the %s payload: %w", eventType, err)
	}

	switch {
	case eventType == "issues" && parsed.Action == "opened":
		return mention{
			Repository: parsed.Repository.FullName,
			Issue:      parsed.Issue.Number,
			Author:     parsed.Issue.User.Login,
			Body:       parsed.Issue.Body,
		}, true, nil
	case eventType == "issue_comment" && parsed.Action == "created":
		return mention{
			Repository: parsed.Repository.FullName,
			Issue:      parsed.Issue.Number,
			Author:     parsed.Comment.User.Login,
			Body:       parsed.Comment.Body,
		}, true, nil
	default:
		return mention{}, false, nil
	}
}

// verifySignature checks GitHub's HMAC over the raw body.
//
// It runs before the body is parsed: an unsigned delivery is not data the bot
// has any business reading.
func verifySignature(secret string, signature string, body []byte) error {
	if secret == "" {
		return fmt.Errorf("no webhook secret is configured, so no delivery can be trusted")
	}
	if !strings.HasPrefix(signature, "sha256=") {
		return fmt.Errorf("the delivery carries no sha256 signature")
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := mac.Sum(nil)

	given, err := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
	if err != nil {
		return fmt.Errorf("the signature is not hexadecimal")
	}
	if !hmac.Equal(expected, given) {
		return fmt.Errorf("the signature does not match the body")
	}
	return nil
}

// Sign produces the header GitHub would send for a body, used by the tests and
// by anyone replaying a delivery by hand.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
