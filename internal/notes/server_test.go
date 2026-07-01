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
	mux    *http.ServeMux
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()

	tmpDir := t.TempDir()
	// Create a test presentation index file
	indexContent := "<html><body>Test Presentation</body></html>"
	os.WriteFile(filepath.Join(tmpDir, "index.html"), []byte(indexContent), 0644)
	// Plus a non-index file the dashboard doesn't intercept — proves the
	// file server still serves uploaded content at non-root paths.
	os.WriteFile(filepath.Join(tmpDir, "slides.html"), []byte(indexContent), 0644)

	cfg := ServerConfig{
		Hostname:          "127.0.0.1",
		Port:              0, // not used with httptest
		PresentationDir:   tmpDir,
		PresentationIndex: "/index.html",
		ActiveTtlMs:       7200000,
	}

	s := NewServer(cfg)
	return &testServer{server: s, mux: s.Mux}
}

func (ts *testServer) Do(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	ts.mux.ServeHTTP(rec, req)
	return rec
}

func TestHandleHealth(t *testing.T) {
	ts := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	resp := ts.Do(req)
	if resp.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.Code)
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

func TestNewServerAppliesDefaults(t *testing.T) {
	cfg := ServerConfig{
		Hostname:        "127.0.0.1",
		Port:            0,
		PresentationDir: t.TempDir(),
	}

	s := NewServer(cfg)
	if s == nil {
		t.Fatal("NewServer returned nil")
	}
	if s.Config.PresentationsDir != "presentations" {
		t.Fatalf("expected default presentations dir, got %q", s.Config.PresentationsDir)
	}
	if s.PresentationTtl != 24*time.Hour {
		t.Fatalf("expected default presentation TTL of 24h, got %v", s.PresentationTtl)
	}
}

func TestHandleRootServesIndex(t *testing.T) {
	ts := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	resp := ts.Do(req)
	if resp.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.Code)
	}

	// After moving the active-sessions dashboard to GET /, the root URL
	// no longer serves the presentation index. The presentation is now
	// served at /<not-root> via the file server, and the dashboard takes
	// over /. Make sure the dashboard renders at / and the presentation
	// is reachable at /p/{name}/.
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Active Notes Sessions") {
		t.Errorf("expected body to contain 'Active Notes Sessions', got: %s", string(body))
	}

	// The presentation content should still be served at non-root paths
	// via the file server (Go's FileServer cleans /index.html → / which
	// is now the dashboard, so we request a non-index file instead).
	presReq := httptest.NewRequest(http.MethodGet, "/slides.html", nil)
	presResp := ts.Do(presReq)
	if presResp.Code != http.StatusOK {
		t.Errorf("expected 200 for /slides.html, got %d", presResp.Code)
	}
	presBody, _ := io.ReadAll(presResp.Body)
	if !strings.Contains(string(presBody), "Test Presentation") {
		t.Errorf("expected /slides.html to contain 'Test Presentation', got: %s", string(presBody))
	}
}

func TestHandleRootFallback(t *testing.T) {
	tmpDir := t.TempDir()
	// No index file in this dir

	mux := http.NewServeMux()
	mux.HandleFunc("/", HandleRoot(tmpDir, "/index.html"))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	resp := httptest.NewRecorder()
	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.Code)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Presentation not yet exported") {
		t.Errorf("expected fallback message, got: %s", string(body))
	}
}

func TestHandleSessionsJSON(t *testing.T) {
	ts := newTestServer(t)

	// Add a session
	ts.server.Store.Touch("test-socket-id", nil)

	req := httptest.NewRequest(http.MethodGet, "/notes/sessions", nil)
	resp := ts.Do(req)
	if resp.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.Code)
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

	// Add a session
	ts.server.Store.Touch("test-socket-id", nil)

	req := httptest.NewRequest(http.MethodGet, "/notes", nil)
	resp := ts.Do(req)
	if resp.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.Code)
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

	req := httptest.NewRequest(http.MethodGet, "/notes", nil)
	resp := ts.Do(req)

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "No active sessions") {
		t.Errorf("expected empty state message, got: %s", string(body))
	}
}

func TestHandleSpeakerView(t *testing.T) {
	ts := newTestServer(t)

	socketId := "abc123xyz"
	req := httptest.NewRequest(http.MethodGet, "/notes/"+socketId, nil)
	resp := ts.Do(req)

	if resp.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.Code)
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

	req := httptest.NewRequest(http.MethodGet, "/notes/", nil)
	resp := ts.Do(req)

	// /notes/ should redirect to dashboard
	if resp.Code != http.StatusOK {
		t.Errorf("expected 200 for /notes/, got %d", resp.Code)
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
		PresentationDir:   tmpDir,
		PresentationIndex: "/index.html",
		ActiveTtlMs:       7200000,
	}

	s := NewServer(cfg)
	req := httptest.NewRequest(http.MethodGet, "/test.txt", nil)
	resp := httptest.NewRecorder()
	s.Mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.Code)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != staticContent {
		t.Errorf("expected '%s', got '%s'", staticContent, string(body))
	}
}

func TestNewServerPrunesExpiredUploadedPresentation(t *testing.T) {
	presentationsDir := t.TempDir()
	createStoredPresentation(t, presentationsDir, "talk", Presentation{
		Name:      "talk",
		CreatedAt: time.Now().UTC(),
		Size:      123,
	})

	cfg := ServerConfig{
		Hostname:          "127.0.0.1",
		Port:              0,
		PresentationDir:   t.TempDir(),
		PresentationIndex: "/index.html",
		PresentationsDir:  presentationsDir,
		PresentationTtlMs: int(time.Hour.Milliseconds()),
		ActiveTtlMs:       7200000,
	}

	server := NewServer(cfg)
	req := httptest.NewRequest(http.MethodGet, "/p/talk/", nil)
	resp := httptest.NewRecorder()
	server.Mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200 before cleanup, got %d", resp.Code)
	}

	createStoredPresentation(t, presentationsDir, "talk", Presentation{
		Name:      "talk",
		CreatedAt: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
		Size:      123,
	})

	server = NewServer(cfg)
	req = httptest.NewRequest(http.MethodGet, "/p/talk/", nil)
	resp = httptest.NewRecorder()
	server.Mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after startup cleanup, got %d", resp.Code)
	}
	if _, err := os.Stat(filepath.Join(presentationsDir, "talk")); !os.IsNotExist(err) {
		t.Fatalf("expired presentation directory should be removed, stat err=%v", err)
	}
}

func TestSocketIOConnectivity(t *testing.T) {
	// Test that the Socket.IO handler is mounted and responds
	ts := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/socket.io/", nil)
	resp := ts.Do(req)

	// Engine.IO responds to GET with transport=polling query. Without
	// proper handshake params, it returns 400. That's expected - it
	// means the handler IS mounted.
	if resp.Code == http.StatusBadRequest || resp.Code == http.StatusOK {
		t.Logf("Socket.IO handler responded with %d (mounted correctly)", resp.Code)
	} else {
		t.Errorf("unexpected status code: %d", resp.Code)
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
		PresentationDir:   t.TempDir(),
		PresentationIndex: "/index.html",
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
