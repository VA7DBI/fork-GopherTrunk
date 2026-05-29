package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/events"
)

type fakeRuntime struct{ dto RuntimeDTO }

func (f fakeRuntime) Runtime() RuntimeDTO { return f.dto }

func TestHandleRuntime_503WhenNotConfigured(t *testing.T) {
	bus := events.NewBus(8)
	s, err := NewServer(ServerOptions{
		Addr: "127.0.0.1:0",
		Bus:  bus,
	})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/runtime", nil)
	s.handleRuntime(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("got %d, want 503", rr.Code)
	}
}

func TestHandleRuntime_ServesDTO(t *testing.T) {
	bus := events.NewBus(8)
	fake := fakeRuntime{dto: RuntimeDTO{
		HTTPAddr:           "127.0.0.1:8080",
		AllowMutations:     true,
		LogLevel:           "info",
		LogFormat:          "text",
		Version:            "v0.0-test",
		AudioEnabled:       true,
		AudioDevice:        "default",
		AudioSampleRate:    8000,
		AudioBackends:      []string{"default", "null"},
		RetentionInterval:  1 * time.Hour,
		VocoderMap:         map[string]string{"p25": "imbe"},
		LastFatalError:     "http: bind: address already in use",
		LastFatalComponent: "http",
		LastFatalAt:        time.Unix(1717000000, 0).UTC(),
		LastFatalClass:     "bind_conflict",
		LastFatalHint:      "A listener port is already in use; change the configured address/port or stop the conflicting process.",
	}}
	s, err := NewServer(ServerOptions{Addr: "127.0.0.1:0", Bus: bus, Runtime: fake})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/runtime", nil)
	s.handleRuntime(rr, req)
	if rr.Code != http.StatusOK {
		body, _ := io.ReadAll(rr.Body)
		t.Fatalf("got %d, want 200: %s", rr.Code, body)
	}
	var out RuntimeDTO
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.HTTPAddr != fake.dto.HTTPAddr {
		t.Errorf("HTTPAddr = %q, want %q", out.HTTPAddr, fake.dto.HTTPAddr)
	}
	if out.VocoderMap["p25"] != "imbe" {
		t.Errorf("vocoder map round-trip lost data: %v", out.VocoderMap)
	}
	if out.RetentionInterval != fake.dto.RetentionInterval {
		t.Errorf("retention interval lost: got %v want %v", out.RetentionInterval, fake.dto.RetentionInterval)
	}
	if out.LastFatalError != fake.dto.LastFatalError || out.LastFatalComponent != fake.dto.LastFatalComponent || !out.LastFatalAt.Equal(fake.dto.LastFatalAt) {
		t.Errorf("fatal metadata lost in runtime DTO round-trip: got=%+v want error=%q component=%q at=%s", out, fake.dto.LastFatalError, fake.dto.LastFatalComponent, fake.dto.LastFatalAt)
	}
	if out.LastFatalClass != fake.dto.LastFatalClass || out.LastFatalHint != fake.dto.LastFatalHint {
		t.Errorf("fatal classification metadata lost in runtime DTO round-trip: class=%q hint=%q", out.LastFatalClass, out.LastFatalHint)
	}
}
