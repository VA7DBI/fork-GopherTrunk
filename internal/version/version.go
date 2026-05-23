// Package version exposes build metadata injected at link time via
// `go build -ldflags "-X ...Version=..." -ldflags "-X ...Commit=..."
// -ldflags "-X ...BuildTime=..."`. Default values keep development
// builds informative without requiring an LDFLAGS dance.
package version

import (
	"runtime/debug"
	"sync"
)

// Version is the human-readable build version. Set at link time by
// the Makefile + release workflow to the git tag (e.g. "v1.0.0") or
// `git describe` output for in-development builds. Defaults to "dev"
// for `go run` / `go test` / bare-`go build` invocations.
var Version = "dev"

// Commit is the short git SHA the binary was built from. Set at link
// time via -ldflags; on a bare `go build` of a git checkout falls
// back to the 7-char prefix of runtime/debug.ReadBuildInfo's
// vcs.revision (with "-dirty" appended when the working tree had
// uncommitted changes at build time). Empty when neither source is
// available — e.g. a tarball build with no .git.
var Commit = ""

// BuildTime is the ISO-8601 UTC timestamp at which the binary was
// built. Set at link time via -ldflags; on a bare `go build` of a
// git checkout falls back to vcs.time from runtime/debug.ReadBuildInfo
// (the HEAD commit timestamp, which is close enough for diagnostics).
// Empty when neither source is available.
var BuildTime = ""

// fallbackOnce guards the one-time read of runtime/debug.ReadBuildInfo
// so concurrent String() callers don't race on the package vars. The
// read happens on first String() call, not in init(), because some
// downstream tests reset Version/Commit/BuildTime around the existing
// String formatter tests and we don't want init() racing those.
var fallbackOnce sync.Once

// String returns a single-line summary suitable for logging or for
// the `gophertrunk version` subcommand: "vX.Y.Z (sha=ABC1234,
// built=2026-05-13T19:00:00Z)". Empty Commit / BuildTime are omitted
// so the most basic dev builds (no git, no -ldflags) still stay
// compact ("dev").
//
// Issue #275 retest cycles repeatedly tripped on stale builds whose
// log lines read `build=dev` and gave no hint of which commit was
// actually loaded. To prevent that recurring without forcing users
// to remember `-ldflags`, on first call we populate empty Commit /
// BuildTime from Go's auto-injected VCS info — explicit `-ldflags`
// still wins (the populate runs only on empty fields).
func String() string {
	fallbackOnce.Do(populateFromBuildInfo)
	out := Version
	if Commit != "" || BuildTime != "" {
		out += " ("
		if Commit != "" {
			out += "sha=" + Commit
		}
		if Commit != "" && BuildTime != "" {
			out += ", "
		}
		if BuildTime != "" {
			out += "built=" + BuildTime
		}
		out += ")"
	}
	return out
}

// populateFromBuildInfo fills empty Commit / BuildTime from Go's
// runtime/debug.ReadBuildInfo VCS settings (available since Go 1.18
// for every `go build` of a git checkout). Skipped per-field when
// -ldflags already set the value, so production builds via `make
// build` and the release workflow are unaffected.
func populateFromBuildInfo() {
	if Commit != "" && BuildTime != "" {
		return // both already set via -ldflags
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	commit, when := extractVCS(info)
	if Commit == "" {
		Commit = commit
	}
	if BuildTime == "" {
		BuildTime = when
	}
}

// extractVCS pulls the short commit SHA and HEAD timestamp out of a
// *debug.BuildInfo's VCS settings. Returns ("", "") if no VCS info
// is present (tarball builds, etc.). Factored out so tests can drive
// the logic with synthetic BuildInfo values without depending on the
// test binary's actual checkout state.
func extractVCS(info *debug.BuildInfo) (commit, when string) {
	if info == nil {
		return "", ""
	}
	var rev, modified, vcsTime string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value
		case "vcs.time":
			vcsTime = s.Value
		}
	}
	if rev != "" {
		short := rev
		if len(short) > 7 {
			short = short[:7]
		}
		if modified == "true" {
			short += "-dirty"
		}
		commit = short
	}
	when = vcsTime
	return commit, when
}
