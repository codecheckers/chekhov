package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/codecheckers/chekhov/internal/check"
	"github.com/codecheckers/chekhov/internal/command"
	"github.com/codecheckers/chekhov/internal/followup"
	"github.com/codecheckers/chekhov/internal/people"
)

// Reminding the handling editor to archive the copy of a checked repository.
//
// `check finish` reads whether the copy in the CODECHECK organisation is
// archived and, when it is not, leaves a record; this is the other end, which
// the nightly sweep runs. The bot never archives anything itself - that needs
// administration rights on every repository in the organisation, far more
// than it should hold - so it reads, and asks. See codecheckers/chekhov#27.
//
// It decides by looking, as every follower does: is the copy archived, is the
// certificate in the register. Nothing to say while the register pull request
// is still open, a reminder once it is merged and every two weeks after, and
// one short confirmation when the copy is archived.

const (
	// archiveEvery is how long a reminder is left before the next.
	archiveEvery = 14 * 24 * time.Hour
	// mostArchiveReminders is how many reminders are sent before the bot
	// stops asking by itself: about three months of them. The last one
	// settles the record, so that `status` does not list it as waiting for
	// ever and the sweep stops reading the copy.
	mostArchiveReminders = 6
)

// archiveFollowUp is what `check finish` says about the copy of the
// repository under check, and the record to leave when it is not archived.
//
// Empty and nil for a repository that is not a copy in the CODECHECK
// organisation - an author's repository, an OSF project, a Zenodo record are
// out of scope and not mentioned - and for a status nobody could read: a
// warning sent because GitHub had a bad minute would be a warning about
// nothing.
func (s *Server) archiveFollowUp(event mention, target string, reading people.Reading,
	services *check.Services) (string, *followup.Record) {
	spec, err := check.ResolveTarget(target, services)
	if err != nil || !check.IsCodecheckCopy(spec) {
		return "", nil
	}
	archive := command.Archive{Repository: spec.Copy().String(), Platform: spec.Type,
		Editor: handlingEditorOr(reading, event.Author)}

	archived, err := services.Archived(spec)
	switch {
	case errors.Is(err, check.ErrArchiveUnknown):
		archive.Unknown = true
		return command.ArchiveWarning(archive), nil
	case err != nil:
		s.Logger.Warn("whether a copy is archived could not be read",
			"issue", event.Issue, "repository", archive.Repository, "error", err)
		return "", nil
	case archived:
		return "", nil
	}

	record := s.followUp(event, followup.Record{Kind: followup.KindArchive,
		Subject: archive.Repository, By: strings.ToLower(event.Author)})
	if record == nil {
		// Nothing would come back to remind anybody, so the warning does not
		// promise it.
		archive.Editor = ""
	}
	return command.ArchiveWarning(archive), record
}

// followArchive resumes an archive reminder, with the services of the walk.
//
// The register first: it is one download for the whole walk, and until the
// row is there nothing is due, so GitHub is asked only about the copies of
// checks that are registered.
func (s *Server) followArchive(ctx context.Context, event mention, record followup.Record,
	services *check.Services) (answered, bool, error) {
	spec, err := check.ParseRepositorySpec(record.Subject)
	if err != nil {
		return answered{}, false, fmt.Errorf("the archive reminder names %q, which I cannot read: %w",
			record.Subject, err)
	}
	row, registered, err := services.RowOfIssue(event.Issue)
	if err != nil {
		return answered{}, false, err
	}
	if !registered {
		// The register pull request is not merged yet: the editor has been
		// told in `check finish`, and the reminders start when it is.
		return answered{}, true, nil
	}

	now := time.Now()
	settled := record
	settled.Settled, settled.Asked = true, stamped(now)
	archived, err := services.Archived(spec)
	switch {
	case check.IsNotFound(err):
		// Deleted or made private: nothing left to archive, and a problem
		// every night about a copy nobody can see would be noise. Said once.
		return answered{body: command.ArchiveGone(record.Subject), follow: &settled}, false, nil
	case err != nil:
		return answered{}, false, err
	case archived:
		return answered{body: command.ArchiveDone(record.Subject), follow: &settled}, false, nil
	case record.Reminders > 0 && !record.Older(archiveEvery, now):
		return answered{}, true, nil
	}

	again := record
	again.Reminders, again.Asked = record.Reminders+1, stamped(now)
	if again.Reminders == mostArchiveReminders {
		// The last: settled, so that nothing waits on a reminder that will
		// not come.
		again.Settled = true
	}
	editor := record.By
	if s.Checks != nil {
		// As the record of the check says now: an editor may have handed the
		// check over since it finished.
		reading, err := s.checks(event).Read(ctx, event.Repository, event.Issue)
		if err == nil {
			editor = handlingEditorOr(reading, record.By)
		}
	}
	archive := command.Archive{Repository: record.Subject, Platform: spec.Type, Editor: editor}
	return answered{
		body:   command.ArchiveReminder(archive, row.Certificate, again.Reminders == mostArchiveReminders),
		follow: &again,
	}, true, nil
}

// handlingEditorOr is whom the archive reminders go to: the handling editor,
// or somebody else when there is none or the record of the check does not
// verify.
func handlingEditorOr(reading people.Reading, otherwise string) string {
	if reading.Tampered == nil && reading.Record.HandlingEditor != "" {
		return reading.Record.HandlingEditor
	}
	return otherwise
}
