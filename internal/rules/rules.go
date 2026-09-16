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
	Updated     string `yaml:"updated"`
	Rules       []Rule `yaml:"rules"`
}

//go:embed data/rules-1.0.yml
var rules10 []byte

//go:embed data/rules-2.0.yml
var rules20 []byte

//go:embed data/provenance.json
var provenanceJSON []byte

// SpecVersions are the specification versions this bot carries rules for,
// newest first. The newest is what a codecheck.yml without a version node is
// validated against, as the specification asks tools to assume.
func SpecVersions() []string { return []string{"2.0", "1.0"} }

// Newest is the specification version assumed when a file names none.
func Newest() string { return SpecVersions()[0] }

var raw = map[string][]byte{"1.0": rules10, "2.0": rules20}

// For returns the rules of one specification version, in file order.
func For(specVersion string) ([]Rule, error) {
	data, ok := raw[specVersion]
	if !ok {
		return nil, fmt.Errorf("no rules for specification version %q", specVersion)
	}
	var parsed catalogue
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("rules-%s.yml: %w", specVersion, err)
	}
	if parsed.SpecVersion != specVersion {
		return nil, fmt.Errorf("rules-%s.yml declares spec_version %q",
			specVersion, parsed.SpecVersion)
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
func Provenance() (ProvenanceRecord, error) {
	var record ProvenanceRecord
	if err := json.Unmarshal(provenanceJSON, &record); err != nil {
		return record, fmt.Errorf("provenance.json: %w", err)
	}
	return record, nil
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
