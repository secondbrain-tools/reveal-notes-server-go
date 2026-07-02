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
	handler := requireAccessToken(newBrowserAuth("secret"), func(w http.ResponseWriter, r *http.Request) {
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

func TestRequireAccessTokenThrottlesFailures(t *testing.T) {
	auth := newBrowserAuth("secret")
	handler := requireAccessToken(auth, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	for i := 0; i < authThrottleFailureLimit; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/presentations", nil)
		req.Header.Set("Authorization", "Bearer wrong")
		resp := httptest.NewRecorder()
		handler(resp, req)
		if resp.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d expected 401, got %d", i+1, resp.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/presentations", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	resp := httptest.NewRecorder()
	handler(resp, req)
	if resp.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after repeated failures, got %d", resp.Code)
	}
	if body := strings.TrimSpace(resp.Body.String()); body != `{"error":"unauthorized"}` {
		t.Fatalf("unexpected throttled body: %s", body)
	}
}

func TestValidatePresentationName(t *testing.T) {
	if err := ValidatePresentationName("talk-1"); err != nil {
		t.Fatalf("expected valid name: %v", err)
	}

	longValid := strings.Repeat("a", 255)
	if err := ValidatePresentationName(longValid); err != nil {
		t.Fatalf("expected 255-char name to be valid: %v", err)
	}

	longInvalid := strings.Repeat("a", 256)
	if err := ValidatePresentationName(longInvalid); err == nil {
		t.Fatalf("expected 256-char name to be invalid")
	}

	for _, name := range []string{"", "../evil", "bad/name"} {
		if err := ValidatePresentationName(name); err == nil {
			t.Fatalf("expected invalid name %q", name)
		}
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

	firstTime := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)
	secondTime := firstTime.Add(1 * time.Hour)

	// Upload first version
	presStore.now = func() time.Time { return firstTime }
	files1 := map[string]string{"index.html": "v1"}
	zip1, _ := createZip(files1)
	body1, ct1 := createMultipartBody("file", "pres.zip", zip1)
	req1, _ := http.NewRequest(http.MethodPost, "/api/presentations/test", body1)
	req1.Header.Set("Content-Type", ct1)
	resp1 := httptest.NewRecorder()
	mux.ServeHTTP(resp1, req1)

	// Upload second version (replaces)
	presStore.now = func() time.Time { return secondTime }
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

	metadataBytes, err := os.ReadFile(filepath.Join(tmpDir, "test", presentationMetadataFilename))
	if err != nil {
		t.Fatal(err)
	}
	var metadata Presentation
	if err := json.Unmarshal(metadataBytes, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Name != "test" {
		t.Fatalf("expected metadata name test, got %q", metadata.Name)
	}
	if !metadata.CreatedAt.Equal(secondTime) {
		t.Fatalf("expected metadata createdAt %v, got %v", secondTime, metadata.CreatedAt)
	}
	if metadata.Size != int64(len(zip2)) {
		t.Fatalf("expected metadata size %d, got %d", len(zip2), metadata.Size)
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

func TestPresentationStoreReloadsPersistedMetadataWithHash(t *testing.T) {
	tmpDir := t.TempDir()
	baseTime := time.Now().UTC()

	store := NewPresentationStore(tmpDir, 24*time.Hour)
	store.now = func() time.Time { return baseTime }

	zipData, _ := createZip(map[string]string{"index.html": "<html>ReloadWithHash</html>"})
	pres, err := store.Add("reload-hash", bytes.NewReader(zipData))
	if err != nil {
		t.Fatal(err)
	}

	if pres.Hash == "" {
		t.Fatal("expected hash to be stored")
	}
	if !strings.HasPrefix(pres.Hash, "sha256:") {
		t.Fatalf("expected hash to start with sha256:, got %q", pres.Hash)
	}

	// Verify hash persisted to disk
	metadataBytes, err := os.ReadFile(filepath.Join(tmpDir, "reload-hash", presentationMetadataFilename))
	if err != nil {
		t.Fatal(err)
	}
	var metadata Presentation
	if err := json.Unmarshal(metadataBytes, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Hash != pres.Hash {
		t.Fatalf("hash mismatch: disk=%q vs mem=%q", metadata.Hash, pres.Hash)
	}

	// Create a second store to reload from disk
	reloaded := NewPresentationStore(tmpDir, 24*time.Hour)
	reloadedHash := reloaded.GetHash("reload-hash")
	if reloadedHash != pres.Hash {
		t.Fatalf("reloaded hash mismatch: %q vs %q", reloadedHash, pres.Hash)
	}

	// Verify full metadata is consistent after reload
	list := reloaded.List()
	if len(list) != 1 || list[0].Name != "reload-hash" {
		t.Fatalf("unexpected list after reload: %+v", list)
	}
}

func TestHandleGetPresentationHash(t *testing.T) {
	tmpDir := t.TempDir()
	presStore := NewPresentationStore(tmpDir, 24*time.Hour)

	zipData, _ := createZip(map[string]string{"index.html": "<html>HashTest</html>"})
	presStore.Add("hash-test", bytes.NewReader(zipData))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/presentations/{name}/hash", HandleGetPresentationHash(presStore))

	req := httptest.NewRequest(http.MethodGet, "/api/presentations/hash-test/hash", nil)
	resp := httptest.NewRecorder()
	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.Code, string(bodyBytes))
	}

	var result struct {
		Name string `json:"name"`
		Hash string `json:"hash"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Name != "hash-test" {
		t.Errorf("expected name hash-test, got %q", result.Name)
	}
	if !strings.HasPrefix(result.Hash, "sha256:") {
		t.Errorf("expected sha256 prefix, got %q", result.Hash)
	}
}

func TestHandleGetPresentationHashNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	presStore := NewPresentationStore(tmpDir, 24*time.Hour)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/presentations/{name}/hash", HandleGetPresentationHash(presStore))

	req := httptest.NewRequest(http.MethodGet, "/api/presentations/nonexistent/hash", nil)
	resp := httptest.NewRecorder()
	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusNotFound {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 404 for missing presentation, got %d: %s", resp.Code, string(bodyBytes))
	}
}

func TestHandleGetPresentationHashRequiresAuth(t *testing.T) {
	tmpDir := t.TempDir()
	presStore := NewPresentationStore(tmpDir, 24*time.Hour)

	zipData, _ := createZip(map[string]string{"index.html": "<html>AuthHash</html>"})
	presStore.Add("auth-hash", bytes.NewReader(zipData))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/presentations/{name}/hash", requireAccessToken(newBrowserAuth("secret-token"), HandleGetPresentationHash(presStore)))

	// Without auth
	req := httptest.NewRequest(http.MethodGet, "/api/presentations/auth-hash/hash", nil)
	resp := httptest.NewRecorder()
	mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth, got %d", resp.Code)
	}

	// With correct auth
	req = httptest.NewRequest(http.MethodGet, "/api/presentations/auth-hash/hash", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	resp = httptest.NewRecorder()
	mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200 with auth, got %d", resp.Code)
	}
}

func TestPresentationStoreGetHash(t *testing.T) {
	tmpDir := t.TempDir()
	presStore := NewPresentationStore(tmpDir, 24*time.Hour)

	zipData, _ := createZip(map[string]string{"index.html": "<html>GetHash</html>"})
	presStore.Add("get-hash", bytes.NewReader(zipData))

	// Existing presentation with hash
	hash := presStore.GetHash("get-hash")
	if hash == "" || !strings.HasPrefix(hash, "sha256:") {
		t.Fatalf("expected sha256 hash, got %q", hash)
	}

	// Non-existent presentation
	if h := presStore.GetHash("does-not-exist"); h != "" {
		t.Fatalf("expected empty hash for non-existent, got %q", h)
	}
}

func TestPresentationStoreReloadsPersistedMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	baseTime := time.Now().UTC()

	store := NewPresentationStore(tmpDir, 24*time.Hour)
	store.now = func() time.Time { return baseTime }

	zipData, _ := createZip(map[string]string{"index.html": "<html>Reload</html>"})
	pres, err := store.Add("reload-me", bytes.NewReader(zipData))
	if err != nil {
		t.Fatal(err)
	}

	metadataBytes, err := os.ReadFile(filepath.Join(tmpDir, "reload-me", presentationMetadataFilename))
	if err != nil {
		t.Fatal(err)
	}
	var metadata Presentation
	if err := json.Unmarshal(metadataBytes, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Name != pres.Name || !metadata.CreatedAt.Equal(pres.CreatedAt) || metadata.Size != pres.Size {
		t.Fatalf("unexpected metadata: %+v vs %+v", metadata, pres)
	}

	reloaded := NewPresentationStore(tmpDir, 24*time.Hour)
	list := reloaded.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 presentation after reload, got %d", len(list))
	}
	if list[0].Name != "reload-me" {
		t.Fatalf("expected reload-me after reload, got %q", list[0].Name)
	}
	if list[0].CreatedAt != pres.CreatedAt.UTC().Format(time.RFC3339) {
		t.Fatalf("expected createdAt %s, got %s", pres.CreatedAt.UTC().Format(time.RFC3339), list[0].CreatedAt)
	}
	if list[0].Size != pres.Size {
		t.Fatalf("expected size %d, got %d", pres.Size, list[0].Size)
	}
}

func TestPresentationStoreStartupCleanupRemovesInvalidAndExpiredEntries(t *testing.T) {
	tmpDir := t.TempDir()
	now := time.Now().UTC()
	ttl := 30 * time.Minute

	createStoredPresentation(t, tmpDir, "keep", Presentation{
		Name:      "keep",
		CreatedAt: now.Add(-10 * time.Minute),
		Size:      1234,
	})
	createStoredPresentation(t, tmpDir, "expired", Presentation{
		Name:      "expired",
		CreatedAt: now.Add(-2 * time.Hour),
		Size:      4321,
	})
	brokenDir := filepath.Join(tmpDir, "broken")
	if err := os.MkdirAll(brokenDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(brokenDir, presentationMetadataFilename), []byte("not-json"), 0644); err != nil {
		t.Fatal(err)
	}

	store := NewPresentationStore(tmpDir, ttl)

	list := store.List()
	if len(list) != 1 || list[0].Name != "keep" {
		t.Fatalf("expected only keep after startup cleanup, got %+v", list)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "expired")); !os.IsNotExist(err) {
		t.Fatalf("expired presentation should be removed, stat err=%v", err)
	}
	if _, err := os.Stat(brokenDir); !os.IsNotExist(err) {
		t.Fatalf("broken presentation should be removed, stat err=%v", err)
	}
}

func TestPresentationStoreRemoveDeletesStaleOnDiskEntry(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewPresentationStore(tmpDir, 24*time.Hour)

	createStoredPresentation(t, tmpDir, "stale", Presentation{
		Name:      "stale",
		CreatedAt: time.Now().UTC(),
		Size:      99,
	})
	store.loadFromDisk()
	delete(store.items, "stale")

	if err := store.Remove("stale"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "stale")); !os.IsNotExist(err) {
		t.Fatalf("stale presentation should be removed, stat err=%v", err)
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

func createStoredPresentation(t *testing.T, baseDir, name string, meta Presentation) {
	t.Helper()
	presDir := filepath.Join(baseDir, name)
	if err := os.MkdirAll(presDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(presDir, "index.html"), []byte("<html>ok</html>"), 0644); err != nil {
		t.Fatal(err)
	}
	meta.Name = name
	meta.Path = ""
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(presDir, presentationMetadataFilename), data, 0644); err != nil {
		t.Fatal(err)
	}
}
