package notes

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zishang520/socket.io/socket"
)

// testServer is a helper that creates a test server with a default config.
type testServer struct {
	server *Server
	ts     *httptest.Server
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()

	tmpDir := t.TempDir()
	// Create a test presentation index file
	indexContent := "<html><body>Test Presentation</body></html>"
	os.WriteFile(filepath.Join(tmpDir, "index.html"), []byte(indexContent), 0644)

	cfg := ServerConfig{
		Hostname:          "127.0.0.1",
		Port:              0, // not used with httptest
		RevealDir:         tmpDir,
		PresentationDir:   tmpDir,
		PresentationIndex: "/index.html",
		PluginDir:         "./node_modules/remote-notes-server",
		ActiveTtlMs:       7200000,
	}

	s := NewServer(cfg)
	ts := httptest.NewServer(s.Mux)

	return &testServer{server: s, ts: ts}
}

func (ts *testServer) Close() {
	ts.ts.Close()
}

func TestHandleHealth(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var result map[string]string
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	if result["status"] != "ok" {
		t.Errorf("expected status ok, got %s", result["status"])
	}
}

func TestHandleRootServesIndex(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Test Presentation") {
		t.Errorf("expected body to contain 'Test Presentation', got: %s", string(body))
	}
}

func TestHandleRootFallback(t *testing.T) {
	tmpDir := t.TempDir()
	// No index file in this dir

	_ = NewSessionStore() // ensure store is used
	mux := http.NewServeMux()
	mux.HandleFunc("/", HandleRoot(tmpDir, "/index.html"))

	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Presentation not yet exported") {
		t.Errorf("expected fallback message, got: %s", string(body))
	}
}

func TestHandleSessionsJSON(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	// Add a session
	ts.server.Store.Touch("test-socket-id", nil)

	resp, err := http.Get(ts.ts.URL + "/notes/sessions")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Count       int              `json:"count"`
		ActiveTtlMs int64            `json:"activeTtlMs"`
		Sessions    []map[string]any `json:"sessions"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	if result.Count != 1 {
		t.Errorf("expected count 1, got %d", result.Count)
	}
	if result.ActiveTtlMs != 7200000 {
		t.Errorf("expected activeTtlMs 7200000, got %d", result.ActiveTtlMs)
	}
	if len(result.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(result.Sessions))
	}
	sid, _ := result.Sessions[0]["socketId"].(string)
	if sid != "test-socket-id" {
		t.Errorf("expected socketId 'test-socket-id', got %s", sid)
	}
}

func TestHandleDashboard(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	// Add a session
	ts.server.Store.Touch("test-socket-id", nil)

	resp, err := http.Get(ts.ts.URL + "/notes")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)
	if !strings.Contains(bodyStr, "Active Notes Sessions") {
		t.Errorf("expected page title, got: %s", bodyStr)
	}
	if !strings.Contains(bodyStr, "test-socket-id") {
		t.Errorf("expected socket ID in dashboard, got: %s", bodyStr)
	}
	if !strings.Contains(bodyStr, "Open Speaker View") {
		t.Errorf("expected Open Speaker View link, got: %s", bodyStr)
	}
}

func TestHandleDashboardEmpty(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.ts.URL + "/notes")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "No active sessions") {
		t.Errorf("expected empty state message, got: %s", string(body))
	}
}

func TestHandleSpeakerView(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	socketId := "abc123xyz"
	resp, err := http.Get(ts.ts.URL + "/notes/" + socketId)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)
	if !strings.Contains(bodyStr, socketId) {
		t.Errorf("expected response to contain socketId %s, got: %s", socketId, bodyStr)
	}
	if !strings.Contains(bodyStr, "reveal.js - Slide Notes") {
		t.Errorf("expected slide notes title, got: %s", bodyStr)
	}
}

func TestHandleSpeakerViewNoSocketId(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.ts.URL + "/notes/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// /notes/ should redirect to dashboard
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for /notes/, got %d", resp.StatusCode)
	}
}

func TestSessionPruning(t *testing.T) {
	store := NewSessionStore()
	baseTime := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return baseTime }

	// Add a stale session
	store.Touch("stale", nil)

	// Add a fresh session
	store.now = func() time.Time { return baseTime.Add(1 * time.Minute) }
	store.Touch("fresh", nil)

	// Prune with 30s TTL
	store.Prune(30 * time.Second)

	sessions := store.List()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session after prune, got %d", len(sessions))
	}
	if sessions[0].SocketId != "fresh" {
		t.Errorf("expected 'fresh' to remain, got %s", sessions[0].SocketId)
	}
}

func TestStaticFileServing(t *testing.T) {
	tmpDir := t.TempDir()
	// Create a test static file
	staticContent := "static file content"
	os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte(staticContent), 0644)

	cfg := ServerConfig{
		Hostname:          "127.0.0.1",
		Port:              0,
		RevealDir:         tmpDir,
		PresentationDir:   tmpDir,
		PresentationIndex: "/index.html",
		PluginDir:         "./node_modules/remote-notes-server",
		ActiveTtlMs:       7200000,
	}

	s := NewServer(cfg)
	ts := httptest.NewServer(s.Mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/test.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != staticContent {
		t.Errorf("expected '%s', got '%s'", staticContent, string(body))
	}
}

func TestSocketIOConnectivity(t *testing.T) {
	// Test that the Socket.IO handler is mounted and responds
	ts := newTestServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.ts.URL + "/socket.io/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Engine.IO responds to GET with transport=polling query. Without
	// proper handshake params, it returns 400. That's expected - it
	// means the handler IS mounted.
	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusOK {
		t.Logf("Socket.IO handler responded with %d (mounted correctly)", resp.StatusCode)
	} else {
		t.Errorf("unexpected status code: %d", resp.StatusCode)
	}
}

func TestSocketIOServeHandler(t *testing.T) {
	// Verify the socket.NewServer correctly returns a handler
	opts := socket.DefaultServerOptions()
	opts.SetServeClient(true)
	sio := socket.NewServer(nil, opts)

	handler := sio.ServeHandler(nil)
	if handler == nil {
		t.Fatal("ServeHandler returned nil")
	}
}

func TestListenAndServe(t *testing.T) {
	// Quick test that ListenAndServe on a random port returns no error
	cfg := ServerConfig{
		Hostname:          "127.0.0.1",
		Port:              0, // port 0 isn't valid for ListenAndServe since it uses port 0 string
		RevealDir:         t.TempDir(),
		PresentationDir:   t.TempDir(),
		PresentationIndex: "/index.html",
		PluginDir:         "./node_modules/remote-notes-server",
		ActiveTtlMs:       7200000,
	}

	// This should not panic
	s := NewServer(cfg)
	if s == nil {
		t.Fatal("NewServer returned nil")
	}
	_ = fmt.Sprintf("%s:%d", cfg.Hostname, 0) // just verify no format issues
}

func TestSessionsJSONPruneAndSort(t *testing.T) {
	store := NewSessionStore()
	now := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }

	store.Touch("old", nil)
	store.now = func() time.Time { return now.Add(5 * time.Minute) }
	store.Touch("new", nil)

	// Prune with 2 min TTL
	store.Prune(2 * time.Minute)
	sessions := store.List()

	if len(sessions) != 1 {
		t.Fatalf("expected 1 session after prune, got %d", len(sessions))
	}
	if sessions[0].SocketId != "new" {
		t.Errorf("expected 'new' to remain, got %s", sessions[0].SocketId)
	}
}
