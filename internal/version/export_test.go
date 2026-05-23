package version

import "sync"

// ResetForTest re-arms the sync.Once that guards populateFromBuildInfo
// so a test can force the fallback path to run again after mutating
// the package-level Commit / BuildTime vars. Test-only.
func ResetForTest() {
	fallbackOnce = sync.Once{}
}

// ExtractVCS exposes the unexported extractVCS helper so tests can
// drive it with synthetic debug.BuildInfo values without depending on
// the test binary's actual checkout state. Test-only.
var ExtractVCS = extractVCS
