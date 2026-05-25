package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/MattCheramie/GopherTrunk/internal/events"
	"github.com/gorilla/websocket"
)

// fakeSpectrumProvider is an in-memory SpectrumProvider for handler tests.
type fakeSpectrumProvider struct {
	devices []SpectrumDevice
	// openErr forces OpenStream to return an error if non-nil.
	openErr error
	// frames is sent on the returned channel one by one with a tiny
	// pause; tests close it via the returned cleanup func.
	frames []SpectrumFrame
}

func (f *fakeSpectrumProvider) Devices() []SpectrumDevice { return f.devices }

func (f *fakeSpectrumProvider) OpenStream(ctx context.Context, serial string, _ int, _ float64) (<-chan SpectrumFrame, func(), error) {
	if f.openErr != nil {
		return nil, nil, f.openErr
	}
	out := make(chan SpectrumFrame, 4)
	streamCtx, cancel := context.WithCancel(ctx)
	go func() {
		defer close(out)
		for _, fr := range f.frames {
			select {
			case <-streamCtx.Done():
				return
			case out <- fr:
			}
			time.Sleep(5 * time.Millisecond)
		}
		// Keep the channel open until ctx cancels so the WS handler
		// stays alive long enough for the test to read the frames.
		<-streamCtx.Done()
	}()
	return out, cancel, nil
}

func newSpectrumTestServer(t *testing.T, prov SpectrumProvider) *httptest.Server {
	t.Helper()
	bus := events.NewBus(8)
	t.Cleanup(bus.Close)
	srv, err := NewServer(ServerOptions{
		Addr:     "127.0.0.1:0",
		Bus:      bus,
		Spectrum: prov,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.routes())
	t.Cleanup(ts.Close)
	return ts
}

func TestSpectrumDevicesReturns503WhenNotWired(t *testing.T) {
	ts := newSpectrumTestServer(t, nil)
	resp, err := http.Get(ts.URL + "/api/v1/spectrum/devices")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestSpectrumDevicesReturnsList(t *testing.T) {
	prov := &fakeSpectrumProvider{
		devices: []SpectrumDevice{
			{Serial: "abc-1", Driver: "rtlsdr", Role: "control", CenterHz: 851_012_500, SampleRateHz: 2_048_000},
			{Serial: "def-2", Driver: "airspy", Role: "voice"},
		},
	}
	ts := newSpectrumTestServer(t, prov)

	resp, err := http.Get(ts.URL + "/api/v1/spectrum/devices")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got []SpectrumDevice
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Serial != "abc-1" || got[0].CenterHz != 851_012_500 {
		t.Errorf("device[0] = %+v", got[0])
	}
}

func TestSpectrumStreamRejectsMissingDevice(t *testing.T) {
	prov := &fakeSpectrumProvider{}
	ts := newSpectrumTestServer(t, prov)

	// HTTP-only sanity probe — WS upgrade requires Upgrade headers.
	// A plain GET against the stream URL should at least *route* to
	// the handler; without WS upgrade gorilla returns 400.
	resp, err := http.Get(ts.URL + "/api/v1/spectrum/stream")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	// 400 (missing device) before WS upgrade is hit when bins / fps
	// would parse fine and only the device check fails — that's the
	// path we want covered.
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (missing device param)", resp.StatusCode)
	}
}

func TestSpectrumStreamRejects503WhenNotWired(t *testing.T) {
	ts := newSpectrumTestServer(t, nil)
	resp, err := http.Get(ts.URL + "/api/v1/spectrum/stream?device=foo")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestSpectrumStreamDeliversFrames(t *testing.T) {
	prov := &fakeSpectrumProvider{
		frames: []SpectrumFrame{
			{TimestampNs: 1, CenterHz: 100, SampleRateHz: 200, Bins: []float32{-50, -40, -30}},
			{TimestampNs: 2, CenterHz: 100, SampleRateHz: 200, Bins: []float32{-45, -35, -25}},
		},
	}
	ts := newSpectrumTestServer(t, prov)

	u, _ := url.Parse(ts.URL)
	wsURL := "ws://" + u.Host + "/api/v1/spectrum/stream?device=any&bins=64&fps=20"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	deadline := time.Now().Add(2 * time.Second)
	conn.SetReadDeadline(deadline)

	for i := 0; i < 2; i++ {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("ReadMessage #%d: %v", i, err)
		}
		var f SpectrumFrame
		if err := json.Unmarshal(msg, &f); err != nil {
			t.Fatalf("unmarshal #%d: %v (raw=%s)", i, err, string(msg))
		}
		if f.CenterHz != 100 {
			t.Errorf("frame #%d CenterHz = %d, want 100", i, f.CenterHz)
		}
		if len(f.Bins) != 3 {
			t.Errorf("frame #%d bins len = %d, want 3", i, len(f.Bins))
		}
	}
}

func TestSpectrumStreamBadBinsRejected(t *testing.T) {
	prov := &fakeSpectrumProvider{}
	ts := newSpectrumTestServer(t, prov)
	// 1000 is not a power of two — handler should bail before WS
	// upgrade.
	resp, err := http.Get(ts.URL + "/api/v1/spectrum/stream?device=any&bins=1000")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	body := make([]byte, 256)
	n, _ := resp.Body.Read(body)
	if !strings.Contains(string(body[:n]), "power of two") {
		t.Errorf("body = %q, want mention of 'power of two'", string(body[:n]))
	}
}
