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

// The deployment platform reaches the container through its configuration and
// nothing else: no -ldflags, no .git to stamp.
func TestTheEnvironmentFillsTheBlanks(t *testing.T) {
	t.Setenv("CHEKHOV_COMMIT", "0123456789abcdef0123")
	t.Setenv("CHEKHOV_VERSION", "")

	version, commit := Stamp("dev", "")
	if commit != "0123456789abcdef0123" {
		t.Errorf("commit = %q, want the configured one", commit)
	}
	if version != "01234567" {
		t.Errorf("version = %q, want it shortened from the commit", version)
	}

	t.Setenv("CHEKHOV_VERSION", "0.2.0")
	if version, _ := Stamp("dev", ""); version != "0.2.0" {
		t.Errorf("version = %q, want the configured one", version)
	}
	if version, _ := Stamp("1.0.0", ""); version != "1.0.0" {
		t.Errorf("version = %q, want the injected one to win", version)
	}
}
