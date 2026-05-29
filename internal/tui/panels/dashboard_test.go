package panels

import (
	"strings"
	"testing"

	"github.com/MattCheramie/GopherTrunk/internal/tui/state"
)

func TestDashboardHealthBodyShowsRuntimeWarningsAndFatalHint(t *testing.T) {
	p := &DashboardPanel{}
	s := &state.SharedState{}
	s.Runtime.StartupWarnings = []string{"talkgroup file missing", "voice SDR missing", "extra warning"}
	s.Runtime.LastFatalClass = "instance_lock"
	s.Runtime.LastFatalError = "another gophertrunk is running"
	s.Runtime.LastFatalHint = "Stop the other process or remove stale lock file."

	body := p.healthBody(s)
	if !strings.Contains(body, "Startup warnings: 3") {
		t.Fatalf("dashboard health body missing warning count: %q", body)
	}
	if !strings.Contains(body, "... 1 more") {
		t.Fatalf("dashboard health body missing warning truncation marker: %q", body)
	}
	if !strings.Contains(body, "Last fatal (instance_lock)") {
		t.Fatalf("dashboard health body missing fatal class: %q", body)
	}
	if !strings.Contains(body, s.Runtime.LastFatalHint) {
		t.Fatalf("dashboard health body missing fatal hint: %q", body)
	}
}
