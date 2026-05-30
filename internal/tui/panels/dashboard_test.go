package panels

import (
	"strings"
	"testing"

	"github.com/MattCheramie/GopherTrunk/internal/tui/client"
	"github.com/MattCheramie/GopherTrunk/internal/tui/state"
)

func TestDashboardPanel_HealthBodyRendersPlutoSummary(t *testing.T) {
	p := NewDashboard()
	s := &state.SharedState{
		Health: client.Health{Status: "ok"},
		Runtime: client.RuntimeDTO{
			SDRBackends: []string{"plutoplus"},
			PlutoRuntime: client.PlutoRuntimeDTO{
				Reconnects:        4,
				DialFailures:      2,
				HandshakeFailures: 1,
				StreamFailures:    3,
			},
		},
	}

	view := p.healthBody(s)
	for _, want := range []string{
		"Pluto Plus: unstable",
		"reconnects 4  failures 6",
		"dial 2",
		"handshake 1",
		"stream 3",
		"hint: check USB/network stability and host performance under load",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("healthBody missing %q in:\n%s", want, view)
		}
	}
}