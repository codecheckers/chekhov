// Package rules holds the CODECHECK validation rules, one set per version of
// the configuration file specification.
//
// The rules are maintained in the register repository, not here:
// https://github.com/codecheckers/register/blob/master/RULES.md
// The files in data/ are copies, refreshed by scripts/update-rules.sh, and
// provenance.json records which register commit they came from.
//
// The identifiers are the contract between this bot and the codecheck R
// package: the same rule has the same identifier in both, so the two
// implementations can be compared by grepping for CC-CFG-016 rather than by
// reading both codebases.
package rules

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Severity of a rule, derived from the RFC 2119 keyword the specification
// uses. It is a property of the rule, never of how a check was invoked.
const (
	SeverityError   = "error"   // a MUST: the configuration is invalid
	SeverityWarning = "warning" // a SHOULD: reported, does not invalidate
	SeverityInfo    = "info"    // a MAY, or advice
)

// A Rule is one entry of the catalogue.
type Rule struct {
	ID          string `yaml:"id"`
	Name        string `yaml:"name"`
	Area        string `yaml:"area"`
	Severity    string `yaml:"severity"`
	Status      string `yaml:"status"`
	Reference   string `yaml:"reference"`
	Description string `yaml:"description"`
}

// Active reports whether the rule is in force, as opposed to withdrawn. A
// withdrawn rule keeps its identifier forever, so it stays in the file.
func (r Rule) Active() bool { return r.Status == "active" }

type catalogue struct {
	Version     int    `yaml:"version"`
	SpecVersion string `yaml:"spec_version"`
	SpecDate    string `yaml:"spec_date"`
	Updated     string `yaml:"updated"`
	Rules       []Rule `yaml:"rules"`
}

//go:embed data/rules-1.0.yml
var rules10 []byte

//go:embed data/rules-2.0.yml
var rules20 []byte

//go:embed data/provenance.json
var provenanceJSON []byte

// bundled is the embedded rule files by name, so that the provenance record
// can be checked against the bytes it claims to describe.
var bundled = map[string][]byte{"rules-1.0.yml": rules10, "rules-2.0.yml": rules20}

// SpecVersions are the specification versions this bot carries rules for,
// newest first. The newest is what a codecheck.yml without a version node is
// validated against, as the specification asks tools to assume.
func SpecVersions() []string { return []string{"2.0", "1.0"} }

// Newest is the specification version assumed when a file names none and
// cannot be dated.
func Newest() string { return SpecVersions()[0] }

// SpecDate is the publication date of a specification version, as the rule
// file records it.
func SpecDate(specVersion string) (time.Time, error) {
	parsed, err := parse(specVersion)
	if err != nil {
		return time.Time{}, err
	}
	if parsed.SpecDate == "" {
		return time.Time{}, fmt.Errorf("rules-%s.yml carries no spec_date", specVersion)
	}
	return time.Parse(time.DateOnly, parsed.SpecDate)
}

// AsOf returns the specification version that was current on a date: the
// newest one published on or before it.
//
// This is what dates a configuration that names no version. Judging a file
// written in 2020 against requirements published in 2026 reports failures its
// author could not have known about; see "Choosing the specification version"
// in the register's RULES.md.
func AsOf(when time.Time) string {
	versions := SpecVersions() // newest first
	dated := false
	for _, version := range versions {
		published, err := SpecDate(version)
		if err != nil {
			continue
		}
		dated = true
		if !published.After(when) {
			return version
		}
	}
	if !dated {
		// No rule file carries a spec_date, so nothing can be dated. Saying
		// "1.0" here would check every modern file against the 2020
		// requirements and call it evidence.
		return ""
	}
	// Older than every specification: the CODECHECK was done before the
	// configuration file was specified at all, and the oldest requirements are
	// the only ones its author could have followed.
	return versions[len(versions)-1]
}

var raw = map[string][]byte{"1.0": rules10, "2.0": rules20}

// parsed caches the bundled rule files: they are compiled in and cannot change
// while the process runs, and the rules are read for every check.
var parsed sync.Map // spec version -> catalogue

// parse reads one bundled rule file.
func parse(specVersion string) (catalogue, error) {
	if cached, ok := parsed.Load(specVersion); ok {
		return cached.(catalogue), nil
	}
	read, err := readCatalogue(specVersion)
	if err != nil {
		return catalogue{}, err
	}
	parsed.Store(specVersion, read)
	return read, nil
}

func readCatalogue(specVersion string) (catalogue, error) {
	data, ok := raw[specVersion]
	if !ok {
		return catalogue{}, fmt.Errorf("no rules for specification version %q", specVersion)
	}
	var read catalogue
	if err := yaml.Unmarshal(data, &read); err != nil {
		return catalogue{}, fmt.Errorf("rules-%s.yml: %w", specVersion, err)
	}
	if read.SpecVersion != specVersion {
		return catalogue{}, fmt.Errorf("rules-%s.yml declares spec_version %q",
			specVersion, read.SpecVersion)
	}
	return read, nil
}

// For returns the rules of one specification version, in file order.
func For(specVersion string) ([]Rule, error) {
	parsed, err := parse(specVersion)
	if err != nil {
		return nil, err
	}
	return parsed.Rules, nil
}

// Get returns one rule by identifier. An unknown identifier is an error rather
// than a zero Rule, because a typo in a rule tag must not pass silently.
func Get(id, specVersion string) (Rule, error) {
	all, err := For(specVersion)
	if err != nil {
		return Rule{}, err
	}
	for _, rule := range all {
		if rule.ID == id {
			return rule, nil
		}
	}
	return Rule{}, fmt.Errorf("no rule %s in the rules for specification %s", id, specVersion)
}

// IDs returns the identifiers of every rule known in any specification
// version, sorted, for coverage checks.
func IDs() ([]string, error) {
	seen := map[string]bool{}
	for _, version := range SpecVersions() {
		all, err := For(version)
		if err != nil {
			return nil, err
		}
		for _, rule := range all {
			seen[rule.ID] = true
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// File records where one bundled rule file came from.
type File struct {
	Name        string `json:"file"`
	SpecVersion string `json:"spec_version"`
	Rules       int    `json:"rules"`
	Commit      string `json:"commit"`
	CommitDate  string `json:"commit_date"`
	MD5         string `json:"md5"`
}

// ProvenanceRecord says which register commit the bundled rules came from and
// when they were fetched. Without it a bundled copy is an undated snapshot.
type ProvenanceRecord struct {
	Comment   string `json:"comment"`
	Source    string `json:"source"`
	Retrieved string `json:"retrieved"`
	Via       string `json:"via"`
	Files     []File `json:"files"`
}

// Provenance reads the record written alongside the bundled rule files.
//
// The record is compiled in and cannot change while the process runs, so it is
// parsed once: the health endpoint asks for it on every probe.
var Provenance = sync.OnceValues(readProvenance)

func readProvenance() (ProvenanceRecord, error) {
	var record ProvenanceRecord
	if err := json.Unmarshal(provenanceJSON, &record); err != nil {
		return record, fmt.Errorf("provenance.json: %w", err)
	}
	return record, nil
}

// SourceRepository is the register the bundled rules were taken from, as
// owner/repo.
//
// Read out of the recorded source rather than written down again: the rules
// always come from the register itself, whichever register a deployment is
// configured to work on, and a bot answering about the testing register still
// judges by codecheckers/register's rules.
func (p ProvenanceRecord) SourceRepository() string {
	rest, found := strings.CutPrefix(p.Source, "https://raw.githubusercontent.com/")
	if !found {
		return ""
	}
	pieces := strings.Split(strings.Trim(rest, "/"), "/")
	if len(pieces) < 2 {
		return ""
	}
	return pieces[0] + "/" + pieces[1]
}

// Commit returns the register commit the bundled files came from, or "" when
// the files disagree, which would mean a half-finished refresh.
func (p ProvenanceRecord) Commit() string {
	commit := ""
	for _, file := range p.Files {
		if commit == "" {
			commit = file.Commit
			continue
		}
		if file.Commit != commit {
			return ""
		}
	}
	return commit
}
