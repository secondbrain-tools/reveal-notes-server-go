package notes

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRequireAccessToken(t *testing.T) {
	handler := requireAccessToken("secret", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/presentations", nil)
	resp := httptest.NewRecorder()
	handler(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.Code)
	}
	if body := strings.TrimSpace(resp.Body.String()); body != `{"error":"unauthorized"}` {
		t.Fatalf("unexpected body: %s", body)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/presentations", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp = httptest.NewRecorder()
	handler(resp, req)

	if resp.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.Code)
	}
}

// createZip creates an in-memory zip with the given files.
func createZip(files map[string]string) ([]byte, error) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := w.Create(name)
		if err != nil {
			return nil, err
		}
		if _, err := f.Write([]byte(content)); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// createMultipartBody creates a multipart form body with a zip file field.
func createMultipartBody(fieldName, filename string, data []byte) (*bytes.Buffer, string) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, _ := w.CreateFormFile(fieldName, filename)
	part.Write(data)
	w.Close()
	return &buf, w.FormDataContentType()
}

func TestHandleUploadPresentation(t *testing.T) {
	tmpDir := t.TempDir()
	presStore := NewPresentationStore(tmpDir, 24*time.Hour)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/presentations/{name}", HandleUploadPresentation(presStore))

	// Create a test zip
	files := map[string]string{
		"index.html":    "<html><body>Test</body></html>",
		"css/style.css": "body { margin: 0; }",
	}
	zipData, err := createZip(files)
	if err != nil {
		t.Fatal(err)
	}

	body, contentType := createMultipartBody("file", "test-pres.zip", zipData)

	req, err := http.NewRequest(http.MethodPost, "/api/presentations/my-talk", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)

	resp := httptest.NewRecorder()
	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 201, got %d: %s", resp.Code, string(bodyBytes))
	}

	// Verify the response JSON
	var info PresentationInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	if info.Name != "my-talk" {
		t.Errorf("expected name 'my-talk', got %q", info.Name)
	}

	// Verify files exist on disk
	presDir := filepath.Join(tmpDir, "my-talk")
	if _, err := os.Stat(presDir); os.IsNotExist(err) {
		t.Fatal("presentation directory not created")
	}
	indexContent, _ := os.ReadFile(filepath.Join(presDir, "index.html"))
	if string(indexContent) != "<html><body>Test</body></html>" {
		t.Errorf("unexpected index.html content: %s", string(indexContent))
	}
	cssContent, _ := os.ReadFile(filepath.Join(presDir, "css", "style.css"))
	if string(cssContent) != "body { margin: 0; }" {
		t.Errorf("unexpected style.css content: %s", string(cssContent))
	}
}

func TestHandleUploadPresentationReplacesExisting(t *testing.T) {
	tmpDir := t.TempDir()
	presStore := NewPresentationStore(tmpDir, 24*time.Hour)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/presentations/{name}", HandleUploadPresentation(presStore))

	// Upload first version
	files1 := map[string]string{"index.html": "v1"}
	zip1, _ := createZip(files1)
	body1, ct1 := createMultipartBody("file", "pres.zip", zip1)
	req1, _ := http.NewRequest(http.MethodPost, "/api/presentations/test", body1)
	req1.Header.Set("Content-Type", ct1)
	resp1 := httptest.NewRecorder()
	mux.ServeHTTP(resp1, req1)

	// Upload second version (replaces)
	files2 := map[string]string{"index.html": "v2"}
	zip2, _ := createZip(files2)
	body2, ct2 := createMultipartBody("file", "pres.zip", zip2)
	req2, _ := http.NewRequest(http.MethodPost, "/api/presentations/test", body2)
	req2.Header.Set("Content-Type", ct2)
	resp2 := httptest.NewRecorder()
	mux.ServeHTTP(resp2, req2)

	content, _ := os.ReadFile(filepath.Join(tmpDir, "test", "index.html"))
	if string(content) != "v2" {
		t.Errorf("expected v2 after replace, got %q", string(content))
	}
}

func TestHandleUploadPresentationInvalidName(t *testing.T) {
	tmpDir := t.TempDir()
	presStore := NewPresentationStore(tmpDir, 24*time.Hour)

	invalidNames := []string{"../evil", "name/with/slash", ""}
	for _, name := range invalidNames {
		files := map[string]string{"index.html": "test"}
		zipData, _ := createZip(files)
		body, contentType := createMultipartBody("file", "pres.zip", zipData)

		req, _ := http.NewRequest(http.MethodPost, "/api/presentations/test", body)
		req.Header.Set("Content-Type", contentType)
		req.SetPathValue("name", name)
		resp := httptest.NewRecorder()
		HandleUploadPresentation(presStore)(resp, req)

		// Invalid names should be rejected before touching disk.
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("name=%q returned %d", name, resp.Code)
		}
	}
}

func TestHandleListPresentations(t *testing.T) {
	tmpDir := t.TempDir()
	presStore := NewPresentationStore(tmpDir, 24*time.Hour)
	presStore.now = func() time.Time { return time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC) }

	// Add a couple presentations
	presStore.Add("talk-a", bytes.NewReader(mustZip(map[string]string{"x": "1"})))
	presStore.now = func() time.Time { return time.Date(2026, 5, 11, 10, 5, 0, 0, time.UTC) }
	presStore.Add("talk-b", bytes.NewReader(mustZip(map[string]string{"x": "2"})))

	req := httptest.NewRequest(http.MethodGet, "/api/presentations", nil)
	resp := httptest.NewRecorder()
	HandleListPresentations(presStore)(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	var result struct {
		Count         int                `json:"count"`
		Presentations []PresentationInfo `json:"presentations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	if result.Count != 2 {
		t.Errorf("expected 2 presentations, got %d", result.Count)
	}
	if len(result.Presentations) != 2 {
		t.Fatalf("expected 2 presentations, got %d", len(result.Presentations))
	}

	// Should be sorted by CreatedAt descending (newest first)
	if result.Presentations[0].Name != "talk-b" {
		t.Errorf("expected talk-b first, got %s", result.Presentations[0].Name)
	}
	if result.Presentations[1].Name != "talk-a" {
		t.Errorf("expected talk-a second, got %s", result.Presentations[1].Name)
	}
}

func TestHandleDeletePresentation(t *testing.T) {
	tmpDir := t.TempDir()
	presStore := NewPresentationStore(tmpDir, 24*time.Hour)

	presStore.Add("to-delete", bytes.NewReader(mustZip(map[string]string{"x": "1"})))

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/presentations/{name}", HandleDeletePresentation(presStore))

	req, _ := http.NewRequest(http.MethodDelete, "/api/presentations/to-delete", nil)
	resp := httptest.NewRecorder()
	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	// Verify it's gone
	if presStore.Get("to-delete") != nil {
		t.Error("presentation should be deleted")
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "to-delete")); !os.IsNotExist(err) {
		t.Error("presentation directory should be removed")
	}
}

func TestHandleServePresentation(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a presentation directory manually
	presDir := filepath.Join(tmpDir, "my-pres")
	os.MkdirAll(presDir, 0755)
	os.WriteFile(filepath.Join(presDir, "index.html"), []byte("<html>Hello</html>"), 0644)
	os.MkdirAll(filepath.Join(presDir, "css"), 0755)
	os.WriteFile(filepath.Join(presDir, "css", "style.css"), []byte("body{}"), 0644)

	// Test serving index.html
	req := httptest.NewRequest(http.MethodGet, "/p/my-pres/", nil)
	resp := httptest.NewRecorder()
	mux := http.NewServeMux()
	mux.HandleFunc("/p/{name}/", HandleServePresentation(tmpDir))
	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Hello") {
		t.Errorf("expected 'Hello' in response, got: %s", string(body))
	}

	// Test serving subdirectory file
	req2 := httptest.NewRequest(http.MethodGet, "/p/my-pres/css/style.css", nil)
	resp2 := httptest.NewRecorder()
	mux.ServeHTTP(resp2, req2)

	if resp2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.Code)
	}
	body2, _ := io.ReadAll(resp2.Body)
	if string(body2) != "body{}" {
		t.Errorf("expected 'body{}', got: %s", string(body2))
	}

	// Test non-existent presentation
	req3 := httptest.NewRequest(http.MethodGet, "/p/nonexistent/", nil)
	resp3 := httptest.NewRecorder()
	mux.ServeHTTP(resp3, req3)
	if resp3.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp3.Code)
	}
}

func TestHandleServePresentationPathTraversal(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a presentation directory
	presDir := filepath.Join(tmpDir, "safe")
	os.MkdirAll(presDir, 0755)
	os.WriteFile(filepath.Join(presDir, "index.html"), []byte("safe"), 0644)

	// Create a file outside the presentation dir
	os.WriteFile(filepath.Join(tmpDir, "secret.txt"), []byte("secret"), 0644)

	// Try path traversal: /p/safe/../secret.txt should not work
	req := httptest.NewRequest(http.MethodGet, "/p/safe/../secret.txt", nil)
	req.SetPathValue("name", "safe")
	resp := httptest.NewRecorder()
	HandleServePresentation(tmpDir)(resp, req)

	// Path traversal should result in 403 Forbidden
	if resp.Code != http.StatusForbidden && resp.Code != http.StatusNotFound {
		t.Errorf("expected 403/404 for traversal, got %d", resp.Code)
	}
}

func TestPresentationPruning(t *testing.T) {
	tmpDir := t.TempDir()
	ttl := 10 * time.Minute
	presStore := NewPresentationStore(tmpDir, ttl)

	baseTime := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)
	presStore.now = func() time.Time { return baseTime }

	// Add a presentation that will be stale
	presStore.Add("stale", bytes.NewReader(mustZip(map[string]string{"x": "1"})))

	// Advance past TTL and add a fresh one
	presStore.now = func() time.Time { return baseTime.Add(20 * time.Minute) }
	presStore.Add("fresh", bytes.NewReader(mustZip(map[string]string{"x": "2"})))

	// Prune
	presStore.Prune()

	// stale should be gone, fresh should remain
	if presStore.Get("stale") != nil {
		t.Error("stale presentation should have been pruned")
	}
	if presStore.Get("fresh") == nil {
		t.Error("fresh presentation should remain")
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "stale")); !os.IsNotExist(err) {
		t.Error("stale presentation directory should be removed")
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
	}
	for _, tc := range tests {
		got := formatBytes(tc.n)
		if got != tc.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

// mustZip creates a zip in memory or panics (helper for tests).
func mustZip(files map[string]string) []byte {
	data, err := createZip(files)
	if err != nil {
		panic(err)
	}
	return data
}
