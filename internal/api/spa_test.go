package api

import (
	"io"
	"net/http"
	"testing"
	"testing/fstest"

	"github.com/MattCheramie/GopherTrunk/internal/events"
)

// fakeSPAFS is a tiny fstest.MapFS that satisfies the embed contract
// for SPA-serving tests.
func fakeSPAFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html": &fstest.MapFile{
			Data: []byte("<!doctype html><html><body>spa-root</body></html>"),
		},
		"assets/app.js": &fstest.MapFile{
			Data: []byte("console.log('hi');"),
		},
	}
}

func TestSPA_RootServesIndex(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	base, teardown := mkServer(t, ServerOptions{
		Bus:       bus,
		WebAssets: fakeSPAFS(),
	})
	defer teardown()

	resp, err := http.Get(base + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !contains(splitLines(string(body)), "spa-root") {
		// Substring check via the helper since contains() in
		// handlers_settings_test takes []string.
		if !bytesContains(body, "spa-root") {
			t.Errorf("body missing 'spa-root', got: %q", string(body))
		}
	}
}

func TestSPA_AssetServesDirectly(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	base, teardown := mkServer(t, ServerOptions{
		Bus:       bus,
		WebAssets: fakeSPAFS(),
	})
	defer teardown()

	resp, err := http.Get(base + "/assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !bytesContains(body, "console.log") {
		t.Errorf("asset body not served correctly, got: %q", string(body))
	}
}

func TestSPA_ClientRouteFallsBackToIndex(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	base, teardown := mkServer(t, ServerOptions{
		Bus:       bus,
		WebAssets: fakeSPAFS(),
	})
	defer teardown()

	resp, err := http.Get(base + "/scanner")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !bytesContains(body, "spa-root") {
		t.Errorf("client route should fall back to index.html, got: %q", string(body))
	}
}

func TestSPA_APIRoutesNotShadowed(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	base, teardown := mkServer(t, ServerOptions{
		Bus:       bus,
		WebAssets: fakeSPAFS(),
		Version:   "test",
	})
	defer teardown()

	resp, err := http.Get(base + "/api/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("api/health status=%d want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if bytesContains(body, "spa-root") {
		t.Errorf("/api/v1/health was shadowed by the SPA handler; body=%q",
			string(body))
	}
}

func TestSPA_NoEmbedReturnsHelpfulMessage(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	base, teardown := mkServer(t, ServerOptions{Bus: bus})
	defer teardown()

	resp, err := http.Get(base + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d want 404 (no embed configured)", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct == "" || !bytesContains([]byte(ct), "text/html") {
		t.Errorf("Content-Type=%q want text/html", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	for _, want := range []string{"make dist", "web console", "/api/v1/health"} {
		if !bytesContains(body, want) {
			t.Errorf("missing-embed body does not mention %q; body=%q", want, string(body))
		}
	}
}

// TestSPA_NoEmbedDoesNotShadowSubpaths confirms the fallback handler
// only attaches to exactly `GET /` — sibling paths like `/scanner`
// must still 404 normally (no fake SPA fallback), and `/api/v1/...`
// must continue to dispatch to its real handler.
func TestSPA_NoEmbedDoesNotShadowSubpaths(t *testing.T) {
	bus := events.NewBus(8)
	defer bus.Close()
	base, teardown := mkServer(t, ServerOptions{Bus: bus, Version: "test"})
	defer teardown()

	resp, err := http.Get(base + "/scanner")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("/scanner status=%d want 404", resp.StatusCode)
	}

	resp, err = http.Get(base + "/api/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("/api/v1/health status=%d want 200 (missing-embed handler must not shadow APIs)", resp.StatusCode)
	}
}

func splitLines(s string) []string {
	out := []string{s}
	return out
}

func bytesContains(b []byte, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(b); i++ {
		if string(b[i:i+len(sub)]) == sub {
			return true
		}
	}
	return false
}
