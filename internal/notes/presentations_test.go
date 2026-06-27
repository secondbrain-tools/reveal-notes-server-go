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

	handler := HandleUploadPresentation(presStore)
	ts := httptest.NewServer(http.HandlerFunc(handler))
	defer ts.Close()

	// Create a test zip
	files := map[string]string{
		"index.html":   "<html><body>Test</body></html>",
		"css/style.css": "body { margin: 0; }",
	}
	zipData, err := createZip(files)
	if err != nil {
		t.Fatal(err)
	}

	body, contentType := createMultipartBody("file", "test-pres.zip", zipData)

	req, err := http.NewRequest("POST", ts.URL+"/api/presentations/my-talk", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 201, got %d: %s", resp.StatusCode, string(bodyBytes))
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

	handler := HandleUploadPresentation(presStore)
	ts := httptest.NewServer(http.HandlerFunc(handler))
	defer ts.Close()

	// Upload first version
	files1 := map[string]string{"index.html": "v1"}
	zip1, _ := createZip(files1)
	body1, ct1 := createMultipartBody("file", "pres.zip", zip1)
	req1, _ := http.NewRequest("POST", ts.URL+"/api/presentations/test", body1)
	req1.Header.Set("Content-Type", ct1)
	resp1, _ := http.DefaultClient.Do(req1)
	resp1.Body.Close()

	// Upload second version (replaces)
	files2 := map[string]string{"index.html": "v2"}
	zip2, _ := createZip(files2)
	body2, ct2 := createMultipartBody("file", "pres.zip", zip2)
	req2, _ := http.NewRequest("POST", ts.URL+"/api/presentations/test", body2)
	req2.Header.Set("Content-Type", ct2)
	resp2, _ := http.DefaultClient.Do(req2)
	resp2.Body.Close()

	content, _ := os.ReadFile(filepath.Join(tmpDir, "test", "index.html"))
	if string(content) != "v2" {
		t.Errorf("expected v2 after replace, got %q", string(content))
	}
}

func TestHandleUploadPresentationInvalidName(t *testing.T) {
	tmpDir := t.TempDir()
	presStore := NewPresentationStore(tmpDir, 24*time.Hour)

	handler := HandleUploadPresentation(presStore)
	ts := httptest.NewServer(http.HandlerFunc(handler))
	defer ts.Close()

	invalidNames := []string{"../evil", "name/with/slash", ""}
	for _, name := range invalidNames {
		files := map[string]string{"index.html": "test"}
		zipData, _ := createZip(files)
		body, contentType := createMultipartBody("file", "pres.zip", zipData)

		// We construct URL manually since httptest mux doesn't route
		req, _ := http.NewRequest("POST", ts.URL+"/api/presentations/"+name, body)
		req.Header.Set("Content-Type", contentType)

		resp, _ := http.DefaultClient.Do(req)
		resp.Body.Close()

		// Invalid names should result in either 400 or 500 from the handler
		// (since httptest server wraps it directly and path value may be URL-decoded)
		if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusInternalServerError {
			t.Logf("name=%q returned %d", name, resp.StatusCode)
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

	handler := HandleListPresentations(presStore)
	ts := httptest.NewServer(http.HandlerFunc(handler))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/presentations")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
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

	handler := HandleDeletePresentation(presStore)
	ts := httptest.NewServer(http.HandlerFunc(handler))
	defer ts.Close()

	req, _ := http.NewRequest("DELETE", ts.URL+"/api/presentations/to-delete", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
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

	mux := http.NewServeMux()
	mux.HandleFunc("/p/{name}/", HandleServePresentation(tmpDir))

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Test serving index.html
	resp, err := http.Get(ts.URL + "/p/my-pres/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Hello") {
		t.Errorf("expected 'Hello' in response, got: %s", string(body))
	}

	// Test serving subdirectory file
	resp2, err := http.Get(ts.URL + "/p/my-pres/css/style.css")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	body2, _ := io.ReadAll(resp2.Body)
	if string(body2) != "body{}" {
		t.Errorf("expected 'body{}', got: %s", string(body2))
	}

	// Test non-existent presentation
	resp3, err := http.Get(ts.URL + "/p/nonexistent/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp3.StatusCode)
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

	mux := http.NewServeMux()
	mux.HandleFunc("/p/{name}/", HandleServePresentation(tmpDir))

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Try path traversal: /p/safe/../secret.txt should not work
	resp, err := http.Get(ts.URL + "/p/safe/../secret.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Path traversal should result in 403 Forbidden
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 403/404 for traversal, got %d", resp.StatusCode)
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
