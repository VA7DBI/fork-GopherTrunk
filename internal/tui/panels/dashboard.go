package panels

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/MattCheramie/GopherTrunk/internal/tui/client"
	"github.com/MattCheramie/GopherTrunk/internal/tui/state"
	"github.com/MattCheramie/GopherTrunk/internal/tui/theme"
)

const plutoRecentWindow = 10 * time.Minute

// DashboardPanel is the at-a-glance landing screen. It is a pure
// renderer over SharedState; it owns no local state.
type DashboardPanel struct{}

// NewDashboard returns the default landing panel.
func NewDashboard() *DashboardPanel { return &DashboardPanel{} }

func (DashboardPanel) Title() string       { return "Dashboard" }
func (DashboardPanel) Keys() []key.Binding { return nil }

func (p *DashboardPanel) Update(_ tea.Msg, _ *state.SharedState) (Panel, tea.Cmd) {
	return p, nil
}

func (p *DashboardPanel) View(width, height int, _ bool, s *state.SharedState) string {
	if width < 4 || height < 4 {
		return ""
	}
	// Below 80 cols the 2×2 grid is unreadable; stack vertically and
	// trim the bottom row to whatever space is left.
	if width < 80 {
		rowH := (height - 1) / 4
		if rowH < 3 {
			rowH = 3
		}
		parts := []string{
			dashboardCard("Health", width, rowH, p.healthBody(s)),
			dashboardCard("Active calls", width, rowH, p.activeBody(s)),
			dashboardCard("Recent events", width, rowH, p.eventsBody(s)),
			dashboardCard("Tone alerts", width, rowH, p.tonesBody(s)),
		}
		return lipgloss.JoinVertical(lipgloss.Left, parts...)
	}
	colW := (width - 2) / 2
	if colW < 20 {
		colW = width - 2
	}
	rowH := (height - 2) / 2
	if rowH < 4 {
		rowH = height - 2
	}

	tl := dashboardCard("Health", colW, rowH, p.healthBody(s))
	tr := dashboardCard("Active calls", colW, rowH, p.activeBody(s))
	bl := dashboardCard("Recent events", colW, rowH, p.eventsBody(s))
	br := dashboardCard("Tone alerts", colW, rowH, p.tonesBody(s))

	top := lipgloss.JoinHorizontal(lipgloss.Top, tl, tr)
	bot := lipgloss.JoinHorizontal(lipgloss.Top, bl, br)
	return lipgloss.JoinVertical(lipgloss.Left, top, bot)
}

func dashboardCard(title string, w, h int, body string) string {
	style := theme.Theme().Frame(false).
		Width(w - 2).
		Height(h - 2)
	header := dashHeader.Render(title)
	return style.Render(header + "\n" + body)
}

func (p *DashboardPanel) healthBody(s *state.SharedState) string {
	var lines []string
	switch {
	case s.HealthErr != nil:
		lines = append(lines, dashErr.Render("daemon unreachable"))
		lines = append(lines, dashDim.Render(s.HealthErr.Error()))
	case s.Health.Status == "":
		lines = append(lines, dashDim.Render("connecting…"))
	default:
		statusStyle := dashOK
		if !strings.EqualFold(s.Health.Status, "ok") {
			statusStyle = dashErr
		}
		lines = append(lines, "Status: "+statusStyle.Render(s.Health.Status))
	}
	if s.Version != "" {
		lines = append(lines, dashDim.Render("Version: "+s.Version))
	}
	if !s.Health.Now.IsZero() {
		lines = append(lines, dashDim.Render("Daemon time: "+s.Health.Now.Format(time.RFC3339)))
	}
	if s.Server != "" {
		lines = append(lines, dashDim.Render("Server: "+s.Server))
	}
	if len(s.Devices) > 0 {
		var control, voice int
		for _, d := range s.Devices {
			switch d.Role {
			case "control":
				control++
			case "voice":
				voice++
			}
		}
		lines = append(lines, dashDim.Render(
			fmt.Sprintf("SDRs: %d (%d control, %d voice)", len(s.Devices), control, voice),
		))
	}
	if len(s.Runtime.StartupWarnings) > 0 {
		lines = append(lines, dashErr.Render(fmt.Sprintf("Startup warnings: %d", len(s.Runtime.StartupWarnings))))
		maxWarnings := len(s.Runtime.StartupWarnings)
		if maxWarnings > 2 {
			maxWarnings = 2
		}
		for i := 0; i < maxWarnings; i++ {
			lines = append(lines, dashDim.Render("! "+s.Runtime.StartupWarnings[i]))
		}
		if len(s.Runtime.StartupWarnings) > maxWarnings {
			lines = append(lines, dashDim.Render(fmt.Sprintf("... %d more", len(s.Runtime.StartupWarnings)-maxWarnings)))
		}
	}
	if plutoDashboardVisible(s.Runtime) {
		pr := s.Runtime.PlutoRuntime
		now := time.Now().UTC()
		severity, label, style := plutoDashboardSeverity(pr, now)
		_ = severity
		lines = append(lines, "Pluto Plus: "+style.Render(label))
		lines = append(lines, dashDim.Render(
			fmt.Sprintf("  reconnects %d  failures %d", pr.Reconnects, plutoFailureTotal(pr)),
		))
		if details := plutoFailureBreakdown(pr); details != "" {
			lines = append(lines, dashDim.Render("  "+details))
		}
		if hint := plutoRemediationHint(pr, now); hint != "" {
			lines = append(lines, dashDim.Render("  hint: "+hint))
		}
	}
	if s.Runtime.LastFatalError != "" {
		label := "Last fatal"
		if s.Runtime.LastFatalClass != "" {
			label += " (" + s.Runtime.LastFatalClass + ")"
		}
		lines = append(lines, dashErr.Render(label))
		if s.Runtime.LastFatalHint != "" {
			lines = append(lines, dashDim.Render(s.Runtime.LastFatalHint))
		}
	}
	return strings.Join(lines, "\n")
}

func plutoDashboardVisible(r client.RuntimeDTO) bool {
	if hasSDRBackend(r.SDRBackends, "plutoplus") {
		return true
	}
	return r.PlutoRuntime.Reconnects > 0 || plutoFailureTotal(r.PlutoRuntime) > 0
}

func plutoFailureTotal(pr client.PlutoRuntimeDTO) uint64 {
	return pr.ReconnectFailures + pr.DialFailures + pr.HandshakeFailures + pr.CommandFailures + pr.StreamFailures + pr.UnknownFailures
}

func plutoFailureBreakdown(pr client.PlutoRuntimeDTO) string {
	parts := make([]string, 0, 5)
	if pr.DialFailures > 0 {
		parts = append(parts, fmt.Sprintf("dial %d", pr.DialFailures))
	}
	if pr.HandshakeFailures > 0 {
		parts = append(parts, fmt.Sprintf("handshake %d", pr.HandshakeFailures))
	}
	if pr.CommandFailures > 0 {
		parts = append(parts, fmt.Sprintf("command %d", pr.CommandFailures))
	}
	if pr.StreamFailures > 0 {
		parts = append(parts, fmt.Sprintf("stream %d", pr.StreamFailures))
	}
	if pr.UnknownFailures > 0 {
		parts = append(parts, fmt.Sprintf("unknown %d", pr.UnknownFailures))
	}
	return strings.Join(parts, "  ·  ")
}

func plutoDashboardSeverity(pr client.PlutoRuntimeDTO, now time.Time) (string, string, lipgloss.Style) {
	failures := plutoFailureTotal(pr)
	recent := plutoFailuresRecent(pr, now)
	switch {
	case failures >= 5 && recent:
		return "err", "unstable", dashErr
	case (failures > 0 && recent) || (pr.Reconnects >= 3 && recent):
		return "warn", "degraded", dashWarn
	case failures > 0:
		return "ok", "historical", dashOK
	default:
		return "ok", "stable", dashOK
	}
}

func plutoRemediationHint(pr client.PlutoRuntimeDTO, now time.Time) string {
	if !plutoFailuresRecent(pr, now) {
		return ""
	}
	stage, count := plutoDominantFailure(pr)
	if count == 0 {
		return ""
	}
	switch stage {
	case "dial":
		return "check Pluto endpoint address/USB transport and device power"
	case "handshake":
		return "verify RTL-TCP compatibility and firmware behavior on connect"
	case "command":
		return "inspect tuner command sequence and Pluto command responses"
	case "stream":
		return "check USB/network stability and host performance under load"
	default:
		return "inspect daemon logs for plutoplus transport error details"
	}
}

func plutoFailuresRecent(pr client.PlutoRuntimeDTO, now time.Time) bool {
	if pr.LastFailureAt.IsZero() {
		return plutoFailureTotal(pr) > 0
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if pr.LastFailureAt.After(now) {
		return true
	}
	return now.Sub(pr.LastFailureAt) <= plutoRecentWindow
}

func plutoDominantFailure(pr client.PlutoRuntimeDTO) (string, uint64) {
	maxStage := ""
	maxCount := uint64(0)
	stages := []struct {
		name  string
		count uint64
	}{
		{name: "dial", count: pr.DialFailures},
		{name: "handshake", count: pr.HandshakeFailures},
		{name: "command", count: pr.CommandFailures},
		{name: "stream", count: pr.StreamFailures},
		{name: "unknown", count: pr.UnknownFailures},
	}
	for _, s := range stages {
		if s.count > maxCount {
			maxCount = s.count
			maxStage = s.name
		}
	}
	return maxStage, maxCount
}

func (p *DashboardPanel) activeBody(s *state.SharedState) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d active\n", len(s.ActiveCalls))
	max := 3
	if len(s.ActiveCalls) < max {
		max = len(s.ActiveCalls)
	}
	for i := 0; i < max; i++ {
		ac := s.ActiveCalls[i]
		alpha := "—"
		if ac.Talkgroup != nil && ac.Talkgroup.AlphaTag != "" {
			alpha = ac.Talkgroup.AlphaTag
		}
		fmt.Fprintf(&b, "%d  %s  %s\n", ac.Grant.GroupID, alpha, ac.Grant.System)
	}
	if len(s.ActiveCalls) == 0 {
		b.WriteString(dashDim.Render("idle"))
	}
	return b.String()
}

func (p *DashboardPanel) eventsBody(s *state.SharedState) string {
	if s.EventLog == nil || s.EventLog.Len() == 0 {
		return dashDim.Render("no events yet")
	}
	latest := s.EventLog.Latest(8)
	var b strings.Builder
	for _, ev := range latest {
		ts := ev.Time.Format("15:04:05")
		fmt.Fprintf(&b, "%s  %s\n", ts, ev.Kind)
	}
	return b.String()
}

func (p *DashboardPanel) tonesBody(s *state.SharedState) string {
	if s.ToneAlerts == nil || s.ToneAlerts.Len() == 0 {
		return dashDim.Render("no tone matches")
	}
	latest := s.ToneAlerts.Latest(5)
	var b strings.Builder
	for _, ev := range latest {
		ts := ev.Time.Format("15:04:05")
		var t client.Tone
		if err := jsonUnmarshal(ev.Raw, &t); err == nil && t.Profile != "" {
			fmt.Fprintln(&b, dashAlert.Render(ts+"  "+t.Profile)+"  "+dashDim.Render(t.DeviceSerial))
			continue
		}
		fmt.Fprintln(&b, dashAlert.Render(ts+"  tone"))
	}
	return b.String()
}
