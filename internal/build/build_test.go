package build

import "testing"

// What -ldflags injected wins: a release build says what it was told to say.
func TestInjectedValuesAreKept(t *testing.T) {
	version, commit := Stamp("1.2.0", "0123456789abcdef")
	if version != "1.2.0" || commit != "0123456789abcdef" {
		t.Errorf("Stamp = %q, %q; want the injected values", version, commit)
	}
}

// Built from a checkout, the binary knows its own revision without anyone
// passing it in - which is the case that matters, because the deployment
// platform pins the build flags.
func TestTheRevisionFillsTheBlanks(t *testing.T) {
	version, commit := Stamp("dev", "")
	if commit == "" {
		t.Skip("this binary carries no VCS stamp")
	}
	if version == "dev" {
		t.Errorf("version = %q, want the revision", version)
	}
	if len(commit) < 8 {
		t.Errorf("commit = %q, want a revision", commit)
	}
}
