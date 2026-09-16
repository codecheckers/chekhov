package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(name string) string {
	return filepath.Join("..", "..", "testdata", name, "codecheck.yml")
}

// The check command has to fail loudly enough for CI: a valid file is a zero
// exit, an invalid one is not.
func TestCheckExitsOnAFailedRule(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"check", fixture("valid-2.0")}, &out); err != nil {
		t.Errorf("a valid file should not be an error: %v", err)
	}
	if !strings.Contains(out.String(), "CC-CFG-001") {
		t.Error("the report should list the rules")
	}

	out.Reset()
	err := run([]string{"check", fixture("placeholders")}, &out)
	if err == nil {
		t.Fatal("an invalid file must be an error, so the exit code is non-zero")
	}
	if !strings.Contains(err.Error(), "CC-CFG-") {
		t.Errorf("the error should name the rules that failed: %v", err)
	}
}

func TestCheckAgainstAnOlderSpecification(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"check", "--spec", "1.0", fixture("no-certificate")}, &out); err != nil {
		t.Errorf("a missing certificate is only advice under 1.0: %v", err)
	}
	out.Reset()
	if err := run([]string{"check", "--spec", "2.0", fixture("no-certificate")}, &out); err == nil {
		t.Error("a missing certificate is a failure under 2.0")
	}
}

func TestCommentRepliesToTheCheckCommand(t *testing.T) {
	var out bytes.Buffer
	comment := t.TempDir() + "/comment.md"
	writeFile(t, comment, "@chekhovbot check "+fixture("valid-2.0")+"\n")

	if err := run([]string{"comment", comment}, &out); err != nil {
		t.Fatalf("comment: %v", err)
	}
	if !strings.Contains(out.String(), "is valid") {
		t.Errorf("the reply should report the outcome:\n%s", out.String())
	}
}

func TestCommentIgnoresWhatIsNotAddressedToTheBot(t *testing.T) {
	var out bytes.Buffer
	comment := t.TempDir() + "/comment.md"
	writeFile(t, comment, "looks good to me\n")

	if err := run([]string{"comment", comment}, &out); err != nil {
		t.Fatalf("comment: %v", err)
	}
	if !strings.Contains(out.String(), "no reply") {
		t.Errorf("a comment that is not addressed to the bot gets no reply:\n%s", out.String())
	}
}

func TestUnknownCommandIsAnError(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"polish"}, &out); err == nil {
		t.Error("an unknown subcommand should be an error")
	}
}

func TestVersionReportsWhereTheRulesCameFrom(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"version"}, &out); err != nil {
		t.Fatalf("version: %v", err)
	}
	for _, want := range []string{"chekhov", "register commit", "rules-2.0.yml"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("version output should mention %q:\n%s", want, out.String())
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// The check command takes a part of the catalogue before the target, the way
// "@chekhovbot check bundle" does.
func TestCheckOnePartOfTheCatalogue(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"check", "bundle", fixture("valid-2.0")}, &out); err != nil {
		t.Fatalf("check bundle: %v", err)
	}
	report := out.String()
	if !strings.Contains(report, "CC-BUN-002") {
		t.Error("check bundle should report the bundle rules")
	}
	if strings.Contains(report, "CC-CFG-001") {
		t.Error("check bundle should not report the configuration rules")
	}
	if !strings.Contains(report, "bundle only") {
		t.Error("the report should say it covered one part")
	}

	// A word that names no part is not silently ignored: it would otherwise be
	// read as a second target and the whole catalogue would run, which is not
	// what was asked for.
	out.Reset()
	err := run([]string{"check", "whatever", fixture("valid-2.0")}, &out)
	if err == nil {
		t.Fatal("an unrecognised word should be an error")
	}
	if !strings.Contains(err.Error(), "one target") {
		t.Errorf("the error should say what it saw: %v", err)
	}
}

// A comment can name a part too.
func TestCommentChecksOnePart(t *testing.T) {
	var out bytes.Buffer
	comment := t.TempDir() + "/comment.md"
	writeFile(t, comment, "@chekhovbot check bundle "+fixture("valid-2.0")+"\n")

	if err := run([]string{"comment", comment}, &out); err != nil {
		t.Fatalf("comment: %v", err)
	}
	if !strings.Contains(out.String(), "bundle only") {
		t.Errorf("the reply should cover the bundle only:\n%s", out.String())
	}
}
