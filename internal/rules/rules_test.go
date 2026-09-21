package rules

import (
	"crypto/md5"
	"fmt"
	"regexp"
	"testing"
	"time"
)

var wellFormedID = regexp.MustCompile(`^CC-(CFG|MET|BUN|REP|REG)-[0-9]{3}$`)

func TestEveryVersionParses(t *testing.T) {
	for _, version := range SpecVersions() {
		catalogue, err := For(version)
		if err != nil {
			t.Fatalf("rules for %s: %v", version, err)
		}
		if len(catalogue) == 0 {
			t.Fatalf("no rules for specification %s", version)
		}

		seen := map[string]bool{}
		for _, rule := range catalogue {
			if !wellFormedID.MatchString(rule.ID) {
				t.Errorf("%s: identifier is not of the form CC-AREA-NNN", rule.ID)
			}
			if seen[rule.ID] {
				t.Errorf("%s: identifier used twice in rules-%s.yml", rule.ID, version)
			}
			seen[rule.ID] = true

			switch rule.Severity {
			case SeverityError, SeverityWarning, SeverityInfo:
			default:
				t.Errorf("%s: unknown severity %q", rule.ID, rule.Severity)
			}
			if rule.Status != "active" && rule.Status != "deprecated" {
				t.Errorf("%s: unknown status %q", rule.ID, rule.Status)
			}
			if rule.Name == "" || rule.Description == "" {
				t.Errorf("%s: needs a name and a description", rule.ID)
			}
		}
	}
}

// Identifiers are permanent, so a rule that appears in both versions is the
// same rule and must not have been renamed.
func TestIdentifiersArePermanent(t *testing.T) {
	older := byID(t, "1.0")
	for _, rule := range mustFor(t, "2.0") {
		previous, ok := older[rule.ID]
		if !ok {
			continue
		}
		if previous.Name != rule.Name {
			t.Errorf("%s: named %q in 1.0 and %q in 2.0", rule.ID, previous.Name, rule.Name)
		}
	}
}

// 2.0 only ever tightens 1.0. This is what makes a 2.0-compliant codecheck.yml
// valid under 1.0 as well.
func TestNewVersionNeverRelaxes(t *testing.T) {
	rank := map[string]int{SeverityInfo: 1, SeverityWarning: 2, SeverityError: 3}
	older := byID(t, "1.0")
	for _, rule := range mustFor(t, "2.0") {
		previous, ok := older[rule.ID]
		if !ok {
			continue
		}
		if rank[rule.Severity] < rank[previous.Severity] {
			t.Errorf("%s: %s in 1.0 but only %s in 2.0",
				rule.ID, previous.Severity, rule.Severity)
		}
	}
}

func TestGetRejectsUnknownIdentifier(t *testing.T) {
	if _, err := Get("CC-CFG-016", "2.0"); err != nil {
		t.Fatalf("CC-CFG-016 should exist: %v", err)
	}
	if _, err := Get("CC-CFG-999", "2.0"); err == nil {
		t.Error("an unknown identifier must be an error, not a zero rule")
	}
}

// The bundled files are copies, so the record of where they came from has to
// agree with what is bundled.
func TestProvenanceMatchesTheBundledFiles(t *testing.T) {
	provenance, err := Provenance()
	if err != nil {
		t.Fatalf("provenance: %v", err)
	}
	if len(provenance.Files) != len(SpecVersions()) {
		t.Fatalf("provenance lists %d files, %d versions are bundled",
			len(provenance.Files), len(SpecVersions()))
	}
	if provenance.Retrieved == "" {
		t.Error("provenance records no retrieval time")
	}
	if provenance.Commit() == "" {
		t.Error("the bundled files come from different register commits")
	}

	// The md5 is what tells a refreshed bundle from a hand-edited one, and
	// what the drift check compares the register's copy against - so it has
	// to describe the bytes that are actually embedded.
	for _, file := range provenance.Files {
		raw, found := bundled[file.Name]
		if !found {
			t.Errorf("provenance names %s, which is not bundled", file.Name)
		} else if sum := fmt.Sprintf("%x", md5.Sum(raw)); sum != file.MD5 {
			t.Errorf("%s: provenance records md5 %s, the bundled file is %s - "+
				"refresh with scripts/update-rules.sh rather than editing by hand",
				file.Name, file.MD5, sum)
		}
	}

	for _, file := range provenance.Files {
		catalogue, err := For(file.SpecVersion)
		if err != nil {
			t.Errorf("%s: %v", file.Name, err)
			continue
		}
		if len(catalogue) != file.Rules {
			t.Errorf("%s: provenance says %d rules, the file holds %d",
				file.Name, file.Rules, len(catalogue))
		}
	}
}

func mustFor(t *testing.T, version string) []Rule {
	t.Helper()
	catalogue, err := For(version)
	if err != nil {
		t.Fatalf("rules for %s: %v", version, err)
	}
	return catalogue
}

func byID(t *testing.T, version string) map[string]Rule {
	t.Helper()
	index := map[string]Rule{}
	for _, rule := range mustFor(t, version) {
		index[rule.ID] = rule
	}
	return index
}

// A file older than every specification is judged by the oldest requirements:
// CODECHECKs were done before the configuration file was specified at all.
func TestAsOfPredatingEverySpecification(t *testing.T) {
	when := time.Date(2019, 2, 14, 0, 0, 0, 0, time.UTC)
	versions := SpecVersions()
	if got := AsOf(when); got != versions[len(versions)-1] {
		t.Errorf("AsOf(2019) = %q, want the oldest version", got)
	}
	if got := AsOf(time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)); got != "2.0" {
		t.Errorf("AsOf(2026-09-30) = %q, want 2.0", got)
	}
}

// Every bundled rule file carries the publication date the version selection
// depends on. Without it a refresh from the register would silently date
// nothing, and every undated file would be checked against the oldest rules.
func TestEverySpecificationHasADate(t *testing.T) {
	for _, version := range SpecVersions() {
		when, err := SpecDate(version)
		if err != nil {
			t.Errorf("rules-%s.yml: %v", version, err)
			continue
		}
		if when.IsZero() {
			t.Errorf("rules-%s.yml has an empty spec_date", version)
		}
	}
}
