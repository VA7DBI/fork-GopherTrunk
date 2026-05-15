package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MattCheramie/GopherTrunk/internal/config"
	tea "github.com/charmbracelet/bubbletea"
)

// wizardStep advances the wizard model by one tea.KeyMsg. Mirrors the
// step() helper in import_tui_test.go so the wizard tests can drive
// the model deterministically.
func wizardStepKey(m configWizardModel, msg tea.Msg) configWizardModel {
	next, _ := m.Update(msg)
	return next.(configWizardModel)
}

func keyRunes(runes ...rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: runes}
}

// TestWizard_DefaultPath_HitEnterThrough simulates an operator who
// holds Enter through every step. The resulting model writes a file
// that internal/config.Load accepts and Validate passes.
func TestWizard_DefaultPath_HitEnterThrough(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config.yaml")

	seed := defaultWizardAnswers()
	seed.ConfigPath = target
	m := newConfigWizard(seed, false)

	// Step through every screen. 13 steps total; pressing Enter on the
	// last field of each advances. Some steps (CORS, SDR devices) have
	// custom Enter semantics — they advance immediately on an empty
	// buffer.
	for safety := 0; safety < 80; safety++ {
		if m.done {
			break
		}
		m = wizardStepKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	}
	if !m.done {
		t.Fatalf("wizard did not reach commit after 80 Enter presses (step=%d)", m.step)
	}
	if !m.wrote {
		t.Fatalf("wizard did not mark wrote=true")
	}

	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read written config: %v", err)
	}
	if !strings.Contains(string(body), "log:\n  level: info") {
		t.Errorf("written config missing log section:\n%s", body)
	}

	cfg, err := config.Load(target)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config.Validate: %v", err)
	}
}

// TestWizard_CORSListBuilder confirms the multi-line list builder
// appends entries on Enter and pops them on Backspace.
func TestWizard_CORSListBuilder(t *testing.T) {
	seed := defaultWizardAnswers()
	m := newConfigWizard(seed, false)

	// Walk to the CORS step (step index 4).
	for m.step < 4 {
		m = wizardStepKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	}
	if m.step != 4 {
		t.Fatalf("expected step=4 (CORS), got %d", m.step)
	}

	// Type "null" and press Enter.
	for _, r := range "null" {
		m = wizardStepKey(m, keyRunes(r))
	}
	m = wizardStepKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := m.answers.CORSAllowedOrigins; len(got) != 1 || got[0] != "null" {
		t.Fatalf("CORS allow-list after add: %v", got)
	}

	// Type a second entry, then Backspace through the whole buffer +
	// one more to pop the committed entry.
	for _, r := range "http://x" {
		m = wizardStepKey(m, keyRunes(r))
	}
	// Backspace 8 times clears the buffer, then once more pops "null".
	for i := 0; i < 9; i++ {
		m = wizardStepKey(m, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	if got := len(m.answers.CORSAllowedOrigins); got != 0 {
		t.Fatalf("CORS allow-list after pop: %d entries, want 0", got)
	}
}

// TestWizard_AbortViaQ confirms pressing q exits without writing.
func TestWizard_AbortViaQ(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "should-not-appear.yaml")

	seed := defaultWizardAnswers()
	seed.ConfigPath = target
	m := newConfigWizard(seed, false)

	final, cmd := m.Update(keyRunes('q'))
	if cmd == nil {
		t.Fatalf("q did not emit a Quit command")
	}
	mm := final.(configWizardModel)
	if mm.wrote {
		t.Errorf("wrote=true after q (should be false)")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("config file written despite q quit: %v", err)
	}
}

// TestWizard_SDRDeviceBuilder commits a device and confirms it lands
// in answers.SDRDevices with the right defaults.
func TestWizard_SDRDeviceBuilder(t *testing.T) {
	seed := defaultWizardAnswers()
	m := newConfigWizard(seed, false)

	// Walk to the SDR step (step index 9).
	for m.step < 9 {
		m = wizardStepKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	}
	// Move past the sample-rate field onto the device builder.
	m = wizardStepKey(m, tea.KeyMsg{Type: tea.KeyTab})
	// Type a serial.
	for _, r := range "00000001" {
		m = wizardStepKey(m, keyRunes(r))
	}
	// Commit the device with Enter.
	m = wizardStepKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := len(m.answers.SDRDevices); got != 1 {
		t.Fatalf("expected 1 committed device, got %d", got)
	}
	d := m.answers.SDRDevices[0]
	if d.Serial != "00000001" {
		t.Errorf("device serial = %q, want 00000001", d.Serial)
	}
	if d.Role != "auto" {
		t.Errorf("device role default = %q, want auto", d.Role)
	}
	if d.Gain != "auto" {
		t.Errorf("device gain default = %q, want auto", d.Gain)
	}
}

// TestExpandConfigPath verifies env-var expansion handles the
// cmd.exe-style %VAR% references Windows operators reach for first,
// the POSIX $VAR / ${VAR} forms, and the leading ~ shorthand. Unknown
// vars are preserved verbatim so a typo doesn't silently become a
// path with empty segments.
func TestExpandConfigPath(t *testing.T) {
	t.Setenv("GTTEST_DIR", "/tmp/gtwiz")
	t.Setenv("GTTEST_NAME", "config.yaml")
	home, herr := os.UserHomeDir()
	if herr != nil {
		t.Skipf("UserHomeDir: %v", herr)
	}

	cases := []struct {
		in   string
		want string
	}{
		{`%GTTEST_DIR%/%GTTEST_NAME%`, "/tmp/gtwiz/config.yaml"},
		{`$GTTEST_DIR/$GTTEST_NAME`, "/tmp/gtwiz/config.yaml"},
		{`${GTTEST_DIR}/${GTTEST_NAME}`, "/tmp/gtwiz/config.yaml"},
		{`~/configs/config.yaml`, filepath.Join(home, "configs/config.yaml")},
		{`./config.yaml`, "./config.yaml"},
		{`%GTTEST_DOES_NOT_EXIST%/config.yaml`, "%GTTEST_DOES_NOT_EXIST%/config.yaml"},
		{`%unterminated/path`, "%unterminated/path"},
	}
	for _, tc := range cases {
		if got := expandConfigPath(tc.in); got != tc.want {
			t.Errorf("expandConfigPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestWizard_WritesAfterEnvVarPath drives the wizard end-to-end with
// a ConfigPath that contains a %VAR% reference. The path the operator
// sees on the review screen contains the unexpanded var, but the
// actual write goes to the resolved location and m.answers.ConfigPath
// is updated so the success message reports the real destination.
func TestWizard_WritesAfterEnvVarPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GTWIZ_TARGET_DIR", dir)

	seed := defaultWizardAnswers()
	seed.ConfigPath = `%GTWIZ_TARGET_DIR%/config.yaml`
	m := newConfigWizard(seed, false)

	for safety := 0; safety < 80; safety++ {
		if m.done {
			break
		}
		m = wizardStepKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	}
	if !m.wrote {
		t.Fatalf("wizard did not write (status=%q)", m.status)
	}
	want := filepath.Join(dir, "config.yaml")
	if m.answers.ConfigPath != want {
		t.Errorf("answers.ConfigPath = %q after commit, want %q (the resolved path, for the success message)", m.answers.ConfigPath, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("file not created at resolved path %s: %v", want, err)
	}
}
