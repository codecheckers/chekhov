package check

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// What the repository under check is, before anybody judges it.
//
// An editor asks this before a codechecker is assigned: what is it written in,
// may it be reused, how big a job is it, and is there a configuration to check
// at all. None of that is a rule - there is no requirement that a repository
// be small - so this is a description rather than a report, and nothing in it
// passes or fails. See codecheckers/chekhov#8.
//
// The facts come from what is already here: Services.Languages, which
// `suggest codecheckers` needed first, and the Bundle, which knows how to
// reach the files of a GitHub, GitLab, OSF or Zenodo repository. Nothing is
// asked of a source in terms it does not have - an archive deposit has no
// language statistics, and saying so is the answer.

// AboutRepository is the word `check` takes for this description.
//
// Deliberately not one of Parts: a part narrows the rule catalogue, and there
// is no catalogue here to narrow.
const AboutRepository = "repository"

// The facts a description reports. They are named so that what could not be
// established is keyed by the same word the answer would have used.
const (
	FactLanguages = "languages"
	FactLicence   = "licence"
	FactConfig    = "codecheck.yml"
	FactSize      = "size"
)

// A Description is what could be established about one repository.
//
// What could not be is in Unknown, with the reason. A repository that states
// no licence and one that could not be reached are different answers, and
// reporting either as the other misleads exactly the person who asked.
type Description struct {
	// Where names what was described, as the bundle itself names it.
	Where string
	// URL is where a reader can see it, empty when there is nowhere.
	URL string

	Languages []string
	// Licence is what the bundle states, empty when it states none.
	Licence string
	// HasConfig is whether there is a codecheck.yml to check.
	HasConfig bool

	// Files and Bytes are what the bundle holds. Bytes counts only the files
	// whose size the listing gave, which Unsized says how many were not.
	Files   int
	Bytes   int64
	Unsized int
	// Partial says the bundle was larger than a description will walk, so the
	// counts are a floor rather than a total.
	Partial bool

	Unknown map[string]string
}

// lister is the one thing measuring a bundle needs: a Bundle is one, and so
// is the memo underneath it.
type lister interface {
	List(dir string) ([]BundleEntry, error)
}

// measureLimits bound the walk. Listing a directory costs a request on three
// of the four sources, and this command answers a question about scale: past
// these the answer is "more than this", which is the same judgement the
// numbers were wanted for.
const (
	measureFileLimit      = 2000
	measureDirectoryLimit = 200
)

// awkward is where a bundle stops being something a codechecker can work
// through in an afternoon. Neither number is a rule; both are worth saying
// out loud before somebody is asked to check it.
const (
	awkwardFiles = 1000
	awkwardBytes = 500 << 20
)

// DescribeRepository reports what can be told about the target.
//
// localPaths is the same permission Load takes: a deployment does not read its
// own disk, and only the command line passes true.
func DescribeRepository(target string, localPaths bool, services *Services) (Description, error) {
	description := Description{Unknown: map[string]string{}}

	bundle, err := description.locate(target, localPaths, services)
	if err != nil {
		return Description{}, err
	}
	// A bundle already knows how to name itself, in the same words the rules
	// use when they report a finding against it.
	description.Where = bundle.Describe()

	description.licenceOf(bundle)
	description.configIn(bundle)
	description.measure(bundle)
	return description, nil
}

// locate decides what is being described and how to reach its files. A
// directory on disk has no languages to report and nowhere to link to; a
// repository has both.
func (d *Description) locate(target string, localPaths bool, services *Services) (Bundle, error) {
	if localPaths && IsLocalPath(target) {
		directory := target
		if info, err := os.Stat(directory); err == nil && !info.IsDir() {
			directory = filepath.Dir(directory)
		}
		d.Unknown[FactLanguages] = "a directory on disk carries no language statistics"
		return newLocalBundle(directory), nil
	}

	spec, bundle, err := bundleOf(target, services)
	if err != nil {
		return nil, err
	}
	d.URL = spec.URL()
	d.languagesOf(spec, services)
	return bundle, nil
}

// languagesOf reads what the repository is written in. A source that cannot
// answer the question at all and one that would not are both unknown, in their
// own words.
func (d *Description) languagesOf(spec RepositorySpec, services *Services) {
	languages, err := services.Languages(spec)
	if err != nil {
		d.Unknown[FactLanguages] = reason(err)
		return
	}
	d.Languages = languages
}

func (d *Description) licenceOf(bundle Bundle) {
	licence, err := bundle.Licence()
	if err != nil {
		d.Unknown[FactLicence] = reason(err)
		return
	}
	d.Licence = licence
}

// configIn asks the listing rather than the file, because the listing is
// already in hand: licenceOf has just read the root, and measure is about to
// read it again from the memo. Bundle.Exists would buy the same answer with a
// request of its own - a HEAD on GitHub, two branch probes on GitLab.
func (d *Description) configIn(bundle Bundle) {
	found, err := existsInListing(bundle, "codecheck.yml")
	if err != nil {
		d.Unknown[FactConfig] = reason(err)
		return
	}
	d.HasConfig = found
}

// measure counts the bundle, breadth first, within the limits.
//
// A directory that cannot be listed does not sink the count: whatever was
// counted is still worth knowing, and the answer says it is a floor. Only a
// root that cannot be listed leaves the size unknown, because then nothing
// was counted at all.
func (d *Description) measure(bundle lister) {
	queue, listings := []string{""}, 0
	for len(queue) > 0 {
		if d.Files >= measureFileLimit || listings >= measureDirectoryLimit {
			d.Partial = true
			return
		}

		directory := queue[0]
		queue = queue[1:]
		// Counted before the call, so that the limit bounds what is asked for
		// rather than what answered: a source that starts refusing would
		// otherwise be asked once for every directory already in the queue.
		listings++
		entries, err := bundle.List(directory)
		if err != nil {
			if errors.Is(err, errNoSuchDirectory) {
				continue
			}
			if listings == 1 {
				d.Unknown[FactSize] = reason(err)
				return
			}
			d.Partial = true
			continue
		}

		for _, entry := range entries {
			if entry.IsDir {
				queue = append(queue, path.Join(directory, entry.Name))
				continue
			}
			d.Files++
			if entry.Size == SizeUnknown {
				d.Unsized++
				continue
			}
			d.Bytes += entry.Size
		}
	}
}

// Awkward reports whether the bundle is large enough to be worth a word before
// somebody is asked to check it.
func (d Description) Awkward() bool {
	return d.Partial || d.Files >= awkwardFiles || d.Bytes >= awkwardBytes
}

// reason is an error in the words the description reports it in. The bundle
// says the outside world is switched off in its own terms, which is true but
// is not what the reader asked.
func reason(err error) string {
	if errors.Is(err, errNoServices) {
		return "this bot is not online"
	}
	return err.Error()
}

// --- saying it -------------------------------------------------------------

// An Answer is one fact and what came of asking for it.
type Answer struct {
	Fact string
	Says string
	// Known is false when the source could not be asked, which reads as a
	// skip rather than as an answer.
	Known bool
}

// Answers are the facts in reporting order.
func (d Description) Answers() []Answer {
	return []Answer{
		d.answer(FactLanguages, listOr(d.Languages, "none the source recognises")),
		d.answer(FactLicence, orElse(d.Licence, "none stated")),
		d.answer(FactConfig, ifElse(d.HasConfig, "present", "not there")),
		d.answer(FactSize, d.size()),
	}
}

// answer is one fact as the report puts it, or the reason it could not be
// established at all.
func (d Description) answer(fact, says string) Answer {
	if why, unknown := d.Unknown[fact]; unknown {
		return Answer{Fact: fact, Says: "could not check: " + why}
	}
	return Answer{Fact: fact, Says: says, Known: true}
}

// established reports whether a fact could be asked for at all, which is a
// different question from what the answer was.
func (d Description) established(fact string) bool {
	_, unknown := d.Unknown[fact]
	return !unknown
}

// Mark is the symbol an answer is shown with: a fact is a note, and a fact
// that could not be established is a skip, as everywhere else.
func (a Answer) Mark() string {
	if !a.Known {
		return Symbol(OutcomeSkipped)
	}
	return Symbol(OutcomeInfo)
}

// size is the count and the bytes in one phrase, honest about both: a listing
// that gave no sizes reports files alone, and a walk that stopped reports what
// it reached.
func (d Description) size() string {
	if d.Files == 0 && !d.Partial {
		return "no files"
	}
	count := fmt.Sprintf("%d files", d.Files)
	if d.Partial {
		count = "more than " + count
	}
	switch {
	case d.Unsized > 0 && d.Unsized == d.Files:
		return count + ", the listing gives no sizes"
	case d.Unsized > 0:
		return fmt.Sprintf("%s, at least %s (%d of them unmeasured)", count, humanBytes(d.Bytes), d.Unsized)
	default:
		return count + ", " + humanBytes(d.Bytes)
	}
}

// humanBytes is a size in the units a person reads.
//
// command.humanBytes says the same thing for the announce reply. The two are
// deliberately apart, as oneLine is: internal/command speaks about replies and
// does not import the checking, and one shared formatter would be the only
// reason for it to. This one counts in int64 and knows about gigabytes,
// because a bundle can be one.
func humanBytes(size int64) string {
	units := []struct {
		suffix string
		scale  int64
	}{{"GB", 1 << 30}, {"MB", 1 << 20}, {"kB", 1 << 10}}
	for _, unit := range units {
		if size >= unit.scale {
			return fmt.Sprintf("%.1f %s", float64(size)/float64(unit.scale), unit.suffix)
		}
	}
	return fmt.Sprintf("%d bytes", size)
}

// listOr is a list as a reader sees it, or what to say when it is empty.
func listOr(values []string, empty string) string {
	if len(values) == 0 {
		return empty
	}
	return strings.Join(values, ", ")
}

func orElse(value, empty string) string {
	if value == "" {
		return empty
	}
	return value
}

func ifElse(condition bool, yes, no string) string {
	if condition {
		return yes
	}
	return no
}

// Text renders the description for a terminal.
func (d Description) Text() string {
	var out strings.Builder
	fmt.Fprintf(&out, "the repository under check: %s\n\n", d.Where)
	for _, answer := range d.Answers() {
		fmt.Fprintf(&out, "%s %s: %s\n", answer.Mark(), answer.Fact, answer.Says)
	}
	if note := d.note(); note != "" {
		fmt.Fprintf(&out, "\n%s\n", note)
	}
	return out.String()
}

// Markdown renders the description as the comment the bot posts.
func (d Description) Markdown() string {
	var out strings.Builder
	where := "`" + d.Where + "`"
	if d.URL != "" {
		where = "[" + where + "](" + d.URL + ")"
	}
	fmt.Fprintf(&out, "About the repository under check, %s:\n\n", where)
	out.WriteString("| | | |\n|---|---|---|\n")
	for _, answer := range d.Answers() {
		fmt.Fprintf(&out, "| %s | %s | %s |\n", answer.Mark(), answer.Fact, escapePipes(answer.Says))
	}
	if note := d.note(); note != "" {
		fmt.Fprintf(&out, "\n%s\n", note)
	}
	out.WriteString("\nThis is a description, not a verdict: nothing here passes or fails. " +
		"`check` validates the `codecheck.yml` against the CODECHECK rules.\n")
	return out.String()
}

// note is the one thing worth saying beyond the facts: whether this is a lot
// to ask of a codechecker, and whether there is anything to check.
func (d Description) note() string {
	var notes []string
	if d.Awkward() {
		notes = append(notes, "This is a large bundle; a codechecker should be told what to expect before being asked.")
	}
	if d.established(FactConfig) && !d.HasConfig {
		notes = append(notes, "There is no `codecheck.yml` yet, so there is nothing for `check` to validate.")
	}
	if d.established(FactLicence) && d.Licence == "" {
		notes = append(notes, "The repository states no licence, which a codechecker needs settled before the check.")
	}
	return strings.Join(notes, " ")
}
