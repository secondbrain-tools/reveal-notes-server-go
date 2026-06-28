package uploader

import (
	"context"
	"encoding/json"
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

func readAllString(t *testing.T, rc io.ReadCloser) string {
	t.Helper()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(data)
}
