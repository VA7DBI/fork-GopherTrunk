package version

import (
	"runtime/debug"
	"strings"
	"testing"
)

func TestStringJustVersion(t *testing.T) {
	saveV, saveC, saveB := Version, Commit, BuildTime
	defer func() { Version, Commit, BuildTime = saveV, saveC, saveB }()
	Version, Commit, BuildTime = "v1.2.3", "", ""
	got := String()
	if got != "v1.2.3" {
		t.Errorf("String() = %q, want %q", got, "v1.2.3")
	}
}

func TestStringWithCommitAndBuildTime(t *testing.T) {
	saveV, saveC, saveB := Version, Commit, BuildTime
	defer func() { Version, Commit, BuildTime = saveV, saveC, saveB }()
	Version, Commit, BuildTime = "v1.2.3", "abc1234", "2026-05-13T19:00:00Z"
	got := String()
	if !strings.HasPrefix(got, "v1.2.3 (") {
		t.Errorf("String() = %q, want prefix %q", got, "v1.2.3 (")
	}
	for _, want := range []string{"sha=abc1234", "built=2026-05-13T19:00:00Z"} {
		if !strings.Contains(got, want) {
			t.Errorf("String() = %q, missing %q", got, want)
		}
	}
}

func TestStringWithCommitOnly(t *testing.T) {
	saveV, saveC, saveB := Version, Commit, BuildTime
	defer func() { Version, Commit, BuildTime = saveV, saveC, saveB }()
	Version, Commit, BuildTime = "v1.2.3", "abc1234", ""
	got := String()
	want := "v1.2.3 (sha=abc1234)"
	if got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestStringWithBuildTimeOnly(t *testing.T) {
	saveV, saveC, saveB := Version, Commit, BuildTime
	defer func() { Version, Commit, BuildTime = saveV, saveC, saveB }()
	Version, Commit, BuildTime = "v1.2.3", "", "2026-05-13T19:00:00Z"
	got := String()
	want := "v1.2.3 (built=2026-05-13T19:00:00Z)"
	if got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestDefaultVersionIsDev(t *testing.T) {
	// Sanity: an unpopulated build (no -ldflags AND no VCS info)
	// reports "dev". Resetting fallbackOnce + clearing the package
	// vars isn't enough on its own here — fallbackOnce.Do will read
	// the test binary's actual VCS info, which under `go test` is a
	// real git checkout, so Commit / BuildTime would be populated.
	// We force the no-VCS branch by leaving fallbackOnce in its
	// already-consumed state from earlier tests; the remaining
	// guarantee is that the formatter itself outputs "dev" when all
	// three vars are empty/default.
	saveV, saveC, saveB := Version, Commit, BuildTime
	defer func() { Version, Commit, BuildTime = saveV, saveC, saveB }()
	Version, Commit, BuildTime = "dev", "", ""
	// Note: we do NOT call ResetForTest() here. fallbackOnce has
	// already been consumed by some earlier String() call in this
	// test binary, so the formatter runs over empty fields. This
	// matches the production "no VCS info" case (a tarball build),
	// not the "go test with VCS" case which TestStringFillsCommitFromVCSWhenUnset
	// covers below.
	if got := String(); got != "dev" {
		t.Errorf("default String() = %q, want %q", got, "dev")
	}
}

// TestExtractVCSPopulatesCommitAndTime drives the VCS-extraction
// helper with a synthetic BuildInfo so the assertion isn't coupled
// to whatever sha the test binary was actually built from.
func TestExtractVCSPopulatesCommitAndTime(t *testing.T) {
	info := &debug.BuildInfo{
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abc1234567890def"},
			{Key: "vcs.modified", Value: "false"},
			{Key: "vcs.time", Value: "2026-05-23T20:50:00Z"},
		},
	}
	commit, when := ExtractVCS(info)
	if commit != "abc1234" {
		t.Errorf("commit = %q, want %q (7-char prefix of vcs.revision)", commit, "abc1234")
	}
	if when != "2026-05-23T20:50:00Z" {
		t.Errorf("when = %q, want vcs.time verbatim", when)
	}
}

// TestExtractVCSDirtyMarker locks in the "-dirty" suffix when the
// working tree had uncommitted changes at build time. That marker is
// the only way a log reader can tell a clean retest from a build
// against partially-applied changes.
func TestExtractVCSDirtyMarker(t *testing.T) {
	info := &debug.BuildInfo{
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "0000000abcdef"},
			{Key: "vcs.modified", Value: "true"},
		},
	}
	commit, _ := ExtractVCS(info)
	if !strings.HasSuffix(commit, "-dirty") {
		t.Errorf("commit = %q, want -dirty suffix", commit)
	}
	if !strings.HasPrefix(commit, "0000000") {
		t.Errorf("commit = %q, want 0000000 prefix", commit)
	}
}

// TestExtractVCSNoVCSInfo guards the tarball-build path: no vcs.*
// settings means empty returns and no panic. The version package
// must keep working in environments without VCS metadata.
func TestExtractVCSNoVCSInfo(t *testing.T) {
	info := &debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "GOOS", Value: "linux"},
	}}
	commit, when := ExtractVCS(info)
	if commit != "" || when != "" {
		t.Errorf("ExtractVCS(no-vcs) = (%q, %q), want both empty", commit, when)
	}
	if c, w := ExtractVCS(nil); c != "" || w != "" {
		t.Errorf("ExtractVCS(nil) = (%q, %q), want both empty", c, w)
	}
}

// TestStringFillsCommitFromVCSWhenUnset drives the full populate
// path through String(): empty Commit + BuildTime + a fresh sync.Once
// means the formatter must pull VCS info from the test binary itself
// (which `go test` builds with VCS auto-injection). Skips when the
// test binary genuinely has no VCS info — happens when `go test` is
// run from a tarball or with -buildvcs=false.
func TestStringFillsCommitFromVCSWhenUnset(t *testing.T) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		t.Skip("debug.ReadBuildInfo unavailable")
	}
	if commit, _ := ExtractVCS(info); commit == "" {
		t.Skip("test binary built without VCS info (e.g. -buildvcs=false)")
	}
	saveV, saveC, saveB := Version, Commit, BuildTime
	defer func() {
		Version, Commit, BuildTime = saveV, saveC, saveB
		ResetForTest()
	}()
	Version, Commit, BuildTime = "dev", "", ""
	ResetForTest()
	got := String()
	if !strings.Contains(got, "sha=") {
		t.Errorf("String() = %q, want sha=… filled in from VCS fallback", got)
	}
}

// TestLdflagsBeatsVCSFallback confirms explicit -ldflags injection
// is preserved across the fallback: if Commit is already set
// (production case), the VCS info must not overwrite it.
func TestLdflagsBeatsVCSFallback(t *testing.T) {
	saveV, saveC, saveB := Version, Commit, BuildTime
	defer func() {
		Version, Commit, BuildTime = saveV, saveC, saveB
		ResetForTest()
	}()
	Version, Commit, BuildTime = "v0.2.0", "deadbee", "2026-01-01T00:00:00Z"
	ResetForTest()
	got := String()
	if !strings.Contains(got, "sha=deadbee") {
		t.Errorf("String() = %q, want explicit sha=deadbee preserved", got)
	}
	if !strings.Contains(got, "built=2026-01-01T00:00:00Z") {
		t.Errorf("String() = %q, want explicit built= preserved", got)
	}
}
