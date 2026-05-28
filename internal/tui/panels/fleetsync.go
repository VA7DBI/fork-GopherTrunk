package panels

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/MattCheramie/GopherTrunk/internal/tui/client"
	"github.com/MattCheramie/GopherTrunk/internal/tui/state"
)

// FleetSyncPanel renders decoded FleetSync events from the live event log.
type FleetSyncPanel struct {
	tbl      table.Model
	filter   textinput.Model
	editing  bool
	details  bool
	mode     fleetSyncFilterMode
	rows     []fleetSyncRow
	lastHash uint64
}

type fleetSyncFilterMode int

const (
	fleetSyncFilterAll fleetSyncFilterMode = iota
	fleetSyncFilterSource
	fleetSyncFilterDestination
	fleetSyncFilterCommand
)

type fleetSyncRow struct {
	msg fleetSyncMessage
	ev  client.Event
}

type fleetSyncMessage struct {
	Timestamp  time.Time `json:"Timestamp"`
	Version    uint8     `json:"Version"`
	Command    uint8     `json:"Command"`
	Subcommand uint8     `json:"Subcommand"`
	FromFleet  uint8     `json:"FromFleet"`
	FromUnit   uint16    `json:"FromUnit"`
	ToFleet    uint8     `json:"ToFleet"`
	ToUnit     uint16    `json:"ToUnit"`
	AllFlag    bool      `json:"AllFlag"`
	Emergency  bool      `json:"Emergency"`
	Priority   bool      `json:"Priority"`
	Payload    []byte    `json:"Payload"`
	RawBytes   []byte    `json:"RawBytes"`
}

func NewFleetSync() *FleetSyncPanel {
	t := table.New(table.WithFocused(true), table.WithColumns(fleetSyncColumns(80)))
	t.SetStyles(tableStyles())
	f := textinput.New()
	f.Placeholder = "filter…"
	f.Width = 24
	f.Prompt = "/"
	return &FleetSyncPanel{tbl: t, filter: f, mode: fleetSyncFilterAll}
}

func (FleetSyncPanel) Title() string { return "FleetSync" }

var (
	fleetSyncFilterKey  = key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter"))
	fleetSyncClearKey   = key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "clear filter"))
	fleetSyncModeKey    = key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "cycle filter field"))
	fleetSyncDetailsKey = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "toggle details"))
	fleetSyncEscKey     = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "exit filter"))
)

func (FleetSyncPanel) Keys() []key.Binding {
	return []key.Binding{fleetSyncFilterKey, fleetSyncModeKey, fleetSyncClearKey, fleetSyncDetailsKey}
}

func (p *FleetSyncPanel) Update(msg tea.Msg, s *state.SharedState) (Panel, tea.Cmd) {
	applyThemeIfChanged(msg, &p.tbl)
	if km, ok := msg.(tea.KeyMsg); ok {
		if p.editing {
			if key.Matches(km, fleetSyncEscKey) {
				p.editing = false
				p.filter.Blur()
				return p, nil
			}
			var cmd tea.Cmd
			p.filter, cmd = p.filter.Update(msg)
			p.refresh(s)
			return p, cmd
		}
		switch {
		case key.Matches(km, fleetSyncFilterKey):
			p.editing = true
			p.filter.Focus()
			return p, textinput.Blink
		case key.Matches(km, fleetSyncClearKey):
			p.filter.SetValue("")
			p.refresh(s)
			return p, nil
		case key.Matches(km, fleetSyncModeKey):
			p.mode = (p.mode + 1) % 4
			p.refresh(s)
			return p, nil
		case key.Matches(km, fleetSyncDetailsKey):
			p.details = !p.details
			return p, nil
		}
	}

	h := p.snapshotHash(s)
	if h != p.lastHash {
		p.refresh(s)
		p.lastHash = h
	}
	var cmd tea.Cmd
	p.tbl, cmd = p.tbl.Update(msg)
	return p, cmd
}

func (p *FleetSyncPanel) View(width, height int, focused bool, s *state.SharedState) string {
	p.tbl.SetColumns(fleetSyncColumns(width))
	p.tbl.SetWidth(width)
	bodyHeight := height - 2
	if bodyHeight < 6 {
		bodyHeight = 6
	}
	detailsBlock := ""
	if p.details {
		detailsBlock = "\n" + p.selectedDetails()
		bodyHeight -= 4
		if bodyHeight < 4 {
			bodyHeight = 4
		}
	}
	p.tbl.SetHeight(bodyHeight)
	header := fmt.Sprintf("%s  mode=%s", p.filter.View(), p.modeLabel())
	body := header + "\n" + p.tbl.View() + detailsBlock
	return panelFrame(fmt.Sprintf("FleetSync (%d)", len(p.rows)), width, height, focused, body)
}

func (p *FleetSyncPanel) HandleMouse(msg tea.MouseMsg, localY int) tea.Cmd {
	handleTableMouse(&p.tbl, msg, localY, 1)
	return nil
}

func (p *FleetSyncPanel) snapshotHash(s *state.SharedState) uint64 {
	if s.EventLog == nil {
		return 0
	}
	events := s.EventLog.Snapshot()
	filtered := make([]client.Event, 0, len(events))
	for _, ev := range events {
		if ev.Kind == "fleetsync.message" {
			filtered = append(filtered, ev)
		}
	}
	return hashRows(filtered, func(ev client.Event) string {
		return ev.Kind + "|" + ev.Time.Format(time.RFC3339Nano) + "|" + string(ev.Raw) + "|" + p.filter.Value() + "|" + p.modeLabel()
	})
}

func (p *FleetSyncPanel) refresh(s *state.SharedState) {
	if s.EventLog == nil {
		p.rows = nil
		p.tbl.SetRows(nil)
		return
	}
	all := s.EventLog.Snapshot()
	rows := make([]fleetSyncRow, 0, len(all))
	for _, ev := range all {
		if ev.Kind != "fleetsync.message" {
			continue
		}
		var m fleetSyncMessage
		if err := jsonUnmarshal(ev.Raw, &m); err != nil {
			continue
		}
		if !p.matchesFilter(m) {
			continue
		}
		rows = append(rows, fleetSyncRow{msg: m, ev: ev})
	}
	sort.Slice(rows, func(i, j int) bool {
		ti := rows[i].msg.Timestamp
		tj := rows[j].msg.Timestamp
		if ti.IsZero() {
			ti = rows[i].ev.Time
		}
		if tj.IsZero() {
			tj = rows[j].ev.Time
		}
		return ti.After(tj)
	})
	p.rows = rows
	trows := make([]table.Row, 0, len(rows))
	for _, r := range rows {
		ts := r.msg.Timestamp
		if ts.IsZero() {
			ts = r.ev.Time
		}
		trows = append(trows, table.Row{
			ts.Format("15:04:05"),
			fleetSyncVersionLabel(r.msg.Version),
			fmt.Sprintf("%d/%d", r.msg.FromFleet, r.msg.FromUnit),
			fmt.Sprintf("%d/%d", r.msg.ToFleet, r.msg.ToUnit),
			fmt.Sprintf("0x%02X", r.msg.Command),
			fmt.Sprintf("0x%02X", r.msg.Subcommand),
			fleetSyncFlags(r.msg),
		})
	}
	p.tbl.SetRows(trows)
}

func (p *FleetSyncPanel) modeLabel() string {
	switch p.mode {
	case fleetSyncFilterSource:
		return "source"
	case fleetSyncFilterDestination:
		return "destination"
	case fleetSyncFilterCommand:
		return "command"
	default:
		return "all"
	}
}

func (p *FleetSyncPanel) matchesFilter(m fleetSyncMessage) bool {
	q := strings.TrimSpace(strings.ToLower(p.filter.Value()))
	if q == "" {
		return true
	}
	src := fmt.Sprintf("%d/%d", m.FromFleet, m.FromUnit)
	dst := fmt.Sprintf("%d/%d", m.ToFleet, m.ToUnit)
	cmd := fmt.Sprintf("0x%02x", m.Command)
	sub := fmt.Sprintf("0x%02x", m.Subcommand)
	switch p.mode {
	case fleetSyncFilterSource:
		return containsFold(src, q)
	case fleetSyncFilterDestination:
		return containsFold(dst, q)
	case fleetSyncFilterCommand:
		return containsFold(cmd, q) || containsFold(sub, q)
	default:
		line := src + " " + dst + " " + cmd + " " + sub + " " + fleetSyncFlags(m)
		return containsFold(line, q)
	}
}

func (p *FleetSyncPanel) selectedDetails() string {
	idx := p.tbl.Cursor()
	if idx < 0 || idx >= len(p.rows) {
		return dashDim.Render("details: no row selected")
	}
	r := p.rows[idx].msg
	payload := "—"
	if len(r.Payload) > 0 {
		payload = strings.ToUpper(hex.EncodeToString(r.Payload))
	}
	raw := "—"
	if len(r.RawBytes) > 0 {
		raw = strings.ToUpper(hex.EncodeToString(r.RawBytes))
	}
	line := fmt.Sprintf("details src=%d/%d dst=%d/%d cmd=0x%02X sub=0x%02X flags=%s payload=%s raw=%s",
		r.FromFleet, r.FromUnit, r.ToFleet, r.ToUnit, r.Command, r.Subcommand, fleetSyncFlags(r), truncate(payload, 48), truncate(raw, 48))
	return dashDim.Render(line)
}

func fleetSyncColumns(w int) []table.Column {
	if w < 70 {
		w = 70
	}
	timeW := 9
	verW := 6
	srcW := 11
	dstW := 11
	cmdW := 6
	subW := 6
	flagsW := w - timeW - verW - srcW - dstW - cmdW - subW - 12
	if flagsW < 8 {
		flagsW = 8
	}
	return []table.Column{
		{Title: "Time", Width: timeW},
		{Title: "Ver", Width: verW},
		{Title: "Source", Width: srcW},
		{Title: "Dest", Width: dstW},
		{Title: "Cmd", Width: cmdW},
		{Title: "Sub", Width: subW},
		{Title: "Flags", Width: flagsW},
	}
}

func fleetSyncVersionLabel(v uint8) string {
	switch v {
	case 2:
		return "FS2"
	default:
		return "FS1"
	}
}

func fleetSyncFlags(m fleetSyncMessage) string {
	flags := make([]string, 0, 3)
	if m.Emergency {
		flags = append(flags, "EMERGENCY")
	}
	if m.AllFlag {
		flags = append(flags, "BROADCAST")
	}
	if m.Priority {
		flags = append(flags, "PRIORITY")
	}
	if len(flags) == 0 {
		return "—"
	}
	return strings.Join(flags, ",")
}
