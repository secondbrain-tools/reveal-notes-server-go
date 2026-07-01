package uploader

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"remote-notes-server/internal/notes"
)

func TestUploadToNotesServerWithBearerAuth(t *testing.T) {
	siteDir := t.TempDir()
	mustWriteFile(t, filepath.Join(siteDir, "index.html"), []byte("<html><body>root</body></html>"))

	uploadsDir := t.TempDir()
	presentationDir := t.TempDir()
	mustWriteFile(t, filepath.Join(presentationDir, "index.html"), []byte("<html><body>placeholder</body></html>"))

	server := notes.NewServer(notes.ServerConfig{
		Hostname:          "127.0.0.1",
		Port:              0,
		PresentationDir:   presentationDir,
		PresentationIndex: "/index.html",
		PresentationsDir:  uploadsDir,
		AccessToken:       "secret-token",
	})
	ts := httptest.NewServer(server.Mux)
	defer ts.Close()

	source := t.TempDir()
	mustWriteFile(t, filepath.Join(source, "presentation.html"), []byte("<html><body><h1>Hello</h1><script src=\"app.js\"></script></body></html>"))
	mustWriteFile(t, filepath.Join(source, "app.js"), []byte("window.answer = 42;"))
	mustWriteFile(t, filepath.Join(source, "ignored.map"), []byte("ignored"))
	mustWriteFile(t, filepath.Join(source, "node_modules", "pkg.js"), []byte("ignored package"))

	archive, err := BuildArchive(ArchiveOptions{
		SourceDir:      source,
		HTMLFile:       "presentation.html",
		IgnorePatterns: []string{"*.map", "node_modules/"},
	})
	if err != nil {
		t.Fatalf("BuildArchive: %v", err)
	}

	client := ts.Client()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	jar, _ := cookiejar.New(nil)
	client.Jar = jar
	resp, err := UploadPresentation(context.Background(), client, ts.URL, "my-talk", archive, "secret-token")
	if err != nil {
		t.Fatalf("UploadPresentation: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload status = %s", resp.Status)
	}

	unauthReq, err := http.NewRequest(http.MethodGet, ts.URL+"/api/presentations", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	unauthResp, err := client.Do(unauthReq)
	if err != nil {
		t.Fatalf("unauth request: %v", err)
	}
	defer unauthResp.Body.Close()
	if unauthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated request, got %s", unauthResp.Status)
	}

	listReq, err := http.NewRequest(http.MethodGet, ts.URL+"/api/presentations", nil)
	if err != nil {
		t.Fatalf("new list request: %v", err)
	}
	listReq.Header.Set("Authorization", "Bearer secret-token")
	listResp, err := client.Do(listReq)
	if err != nil {
		t.Fatalf("list request: %v", err)
	}
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %s", listResp.Status)
	}
	var listResult struct {
		Count         int `json:"count"`
		Presentations []struct {
			Name string `json:"name"`
		} `json:"presentations"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&listResult); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if listResult.Count != 1 || len(listResult.Presentations) != 1 || listResult.Presentations[0].Name != "my-talk" {
		t.Fatalf("unexpected list result: %+v", listResult)
	}

	loginForm := url.Values{}
	loginForm.Set("token", "secret-token")
	loginForm.Set("returnTo", "/p/my-talk/")
	loginReq, err := http.NewRequest(http.MethodPost, ts.URL+"/login", strings.NewReader(loginForm.Encode()))
	if err != nil {
		t.Fatalf("new login request: %v", err)
	}
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginResp, err := client.Do(loginReq)
	if err != nil {
		t.Fatalf("login request: %v", err)
	}
	if loginResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login status = %s", loginResp.Status)
	}
	loginResp.Body.Close()

	renderResp, err := client.Get(ts.URL + "/p/my-talk/")
	if err != nil {
		t.Fatalf("fetch presentation: %v", err)
	}
	defer renderResp.Body.Close()
	if renderResp.StatusCode != http.StatusOK {
		t.Fatalf("render status = %s", renderResp.Status)
	}
	body := readAllString(t, renderResp.Body)
	if !strings.Contains(body, "<h1>Hello</h1>") {
		t.Fatalf("presentation html not served: %s", body)
	}

	assetResp, err := client.Get(ts.URL + "/p/my-talk/app.js")
	if err != nil {
		t.Fatalf("fetch asset: %v", err)
	}
	defer assetResp.Body.Close()
	if assetResp.StatusCode != http.StatusOK {
		t.Fatalf("asset status = %s", assetResp.Status)
	}
	if got := readAllString(t, assetResp.Body); got != "window.answer = 42;" {
		t.Fatalf("asset content = %q", got)
	}

	missingResp, err := client.Get(ts.URL + "/p/my-talk/ignored.map")
	if err != nil {
		t.Fatalf("fetch ignored asset: %v", err)
	}
	defer missingResp.Body.Close()
	if missingResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected ignored file to be absent, got %s", missingResp.Status)
	}
}

func TestUploadSkippedOnHashMatch(t *testing.T) {
	uploadsDir := t.TempDir()
	presentationDir := t.TempDir()

	server := notes.NewServer(notes.ServerConfig{
		Hostname:          "127.0.0.1",
		Port:              0,
		PresentationDir:   presentationDir,
		PresentationIndex: "/index.html",
		PresentationsDir:  uploadsDir,
		AccessToken:       "secret-token",
	})
	ts := httptest.NewServer(server.Mux)
	defer ts.Close()

	source := t.TempDir()
	mustWriteFile(t, filepath.Join(source, "presentation.html"), []byte("<html><body><h1>Skipped</h1></body></html>"))
	mustWriteFile(t, filepath.Join(source, "app.js"), []byte("window.answer = 42;"))

	archive, err := BuildArchive(ArchiveOptions{
		SourceDir: source,
		HTMLFile:  "presentation.html",
	})
	if err != nil {
		t.Fatalf("BuildArchive: %v", err)
	}

	client := ts.Client()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

	// First upload: should succeed
	resp, err := UploadPresentation(context.Background(), client, ts.URL, "skip-test", archive, "secret-token")
	if err != nil {
		t.Fatalf("first UploadPresentation: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first upload status = %s", resp.Status)
	}

	// Check that the remote hash matches the local hash
	localHash := fmt.Sprintf("sha256:%x", sha256.Sum256(archive))
	remoteHash, err := FetchRemoteHash(context.Background(), client, ts.URL, "skip-test", "secret-token")
	if err != nil {
		t.Fatalf("FetchRemoteHash: %v", err)
	}
	if remoteHash != localHash {
		t.Fatalf("hash mismatch: remote=%q local=%q", remoteHash, localHash)
	}

	// Second upload of identical content: hash should match, so we skip
	remoteHash2, err := FetchRemoteHash(context.Background(), client, ts.URL, "skip-test", "secret-token")
	if err != nil {
		t.Fatalf("second FetchRemoteHash: %v", err)
	}
	if remoteHash2 != localHash {
		t.Fatalf("second hash check should match: remote=%q local=%q", remoteHash2, localHash)
	}

	// Verify the presentation is still listed
	listReq, err := http.NewRequest(http.MethodGet, ts.URL+"/api/presentations", nil)
	if err != nil {
		t.Fatalf("new list request: %v", err)
	}
	listReq.Header.Set("Authorization", "Bearer secret-token")
	listResp, err := client.Do(listReq)
	if err != nil {
		t.Fatalf("list request: %v", err)
	}
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %s", listResp.Status)
	}
	var listResult struct {
		Count int `json:"count"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&listResult); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if listResult.Count != 1 {
		t.Fatalf("expected 1 presentation, got %d", listResult.Count)
	}
}

func TestUploadProceedsOnHashDifference(t *testing.T) {
	uploadsDir := t.TempDir()
	presentationDir := t.TempDir()

	server := notes.NewServer(notes.ServerConfig{
		Hostname:          "127.0.0.1",
		Port:              0,
		PresentationDir:   presentationDir,
		PresentationIndex: "/index.html",
		PresentationsDir:  uploadsDir,
		AccessToken:       "secret-token",
	})
	ts := httptest.NewServer(server.Mux)
	defer ts.Close()

	source := t.TempDir()
	mustWriteFile(t, filepath.Join(source, "presentation.html"), []byte("<html><body><h1>Version 1</h1></body></html>"))

	archive1, err := BuildArchive(ArchiveOptions{
		SourceDir: source,
		HTMLFile:  "presentation.html",
	})
	if err != nil {
		t.Fatalf("BuildArchive v1: %v", err)
	}

	client := ts.Client()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

	// First upload
	resp1, err := UploadPresentation(context.Background(), client, ts.URL, "diff-test", archive1, "secret-token")
	if err != nil {
		t.Fatalf("first UploadPresentation: %v", err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusCreated {
		t.Fatalf("first upload status = %s", resp1.Status)
	}

	// Change the content
	mustWriteFile(t, filepath.Join(source, "presentation.html"), []byte("<html><body><h1>Version 2 Modified</h1></body></html>"))

	archive2, err := BuildArchive(ArchiveOptions{
		SourceDir: source,
		HTMLFile:  "presentation.html",
	})
	if err != nil {
		t.Fatalf("BuildArchive v2: %v", err)
	}

	// Hashes should differ
	localHash1 := fmt.Sprintf("sha256:%x", sha256.Sum256(archive1))
	localHash2 := fmt.Sprintf("sha256:%x", sha256.Sum256(archive2))
	if localHash1 == localHash2 {
		t.Fatalf("hashes should differ after content change")
	}

	// Fetch remote hash (should be v1 hash)
	remoteHash, err := FetchRemoteHash(context.Background(), client, ts.URL, "diff-test", "secret-token")
	if err != nil {
		t.Fatalf("FetchRemoteHash: %v", err)
	}
	if remoteHash != localHash1 {
		t.Fatalf("remote hash should match v1: remote=%q local=%q", remoteHash, localHash1)
	}
	if remoteHash == localHash2 {
		t.Fatalf("remote hash should NOT match v2")
	}

	// Second upload with different content: should succeed
	resp2, err := UploadPresentation(context.Background(), client, ts.URL, "diff-test", archive2, "secret-token")
	if err != nil {
		t.Fatalf("second UploadPresentation: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusCreated {
		t.Fatalf("second upload status = %s", resp2.Status)
	}

	// Remote hash should now be v2
	remoteHashAfter, err := FetchRemoteHash(context.Background(), client, ts.URL, "diff-test", "secret-token")
	if err != nil {
		t.Fatalf("FetchRemoteHash after second upload: %v", err)
	}
	if remoteHashAfter != localHash2 {
		t.Fatalf("remote hash should match v2 after upload: remote=%q local=%q", remoteHashAfter, localHash2)
	}
}

func TestFetchRemoteHashNotFound(t *testing.T) {
	uploadsDir := t.TempDir()
	presentationDir := t.TempDir()

	server := notes.NewServer(notes.ServerConfig{
		Hostname:          "127.0.0.1",
		Port:              0,
		PresentationDir:   presentationDir,
		PresentationIndex: "/index.html",
		PresentationsDir:  uploadsDir,
		AccessToken:       "secret-token",
	})
	ts := httptest.NewServer(server.Mux)
	defer ts.Close()

	client := ts.Client()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

	// Fetch hash for non-existent presentation
	hash, err := FetchRemoteHash(context.Background(), client, ts.URL, "does-not-exist", "secret-token")
	if err != nil {
		t.Fatalf("FetchRemoteHash for missing: %v", err)
	}
	if hash != "" {
		t.Fatalf("expected empty hash for missing presentation, got %q", hash)
	}
}

func readAllString(t *testing.T, rc io.ReadCloser) string {
	t.Helper()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(data)
}
