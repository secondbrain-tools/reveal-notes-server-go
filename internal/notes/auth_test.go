package notes

import (
	"bytes"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newAuthTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()

	siteDir := t.TempDir()
	mustWriteFile(t, filepath.Join(siteDir, "index.html"), []byte("<html><body>root</body></html>"))
	mustWriteFile(t, filepath.Join(siteDir, "app.js"), []byte("window.site = true;"))

	uploadsDir := t.TempDir()
	server := NewServer(ServerConfig{
		Hostname:          "127.0.0.1",
		Port:              0,
		PresentationDir:   siteDir,
		PresentationIndex: "/index.html",
		PresentationsDir:  uploadsDir,
		AccessToken:       "secret-token",
	})

	ts := httptest.NewServer(server.Mux)
	t.Cleanup(ts.Close)
	return ts, "secret-token"
}

func mustWriteFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func noRedirectClient(ts *httptest.Server) *http.Client {
	client := ts.Client()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return client
}

func uploadProtectedPresentation(t *testing.T, client *http.Client, baseURL, token string) {
	t.Helper()

	body, contentType := createMultipartBody("file", "talk.zip", mustZip(map[string]string{
		"index.html": "<html><body><h1>Hello</h1></body></html>",
		"app.js":     "window.answer = 42;",
	}))
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/presentations/my-talk", body)
	if err != nil {
		t.Fatalf("new upload request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("upload request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload status = %s: %s", resp.Status, string(b))
	}
}

func TestBrowserAuthFlow(t *testing.T) {
	ts, token := newAuthTestServer(t)
	client := noRedirectClient(ts)
	jar, _ := cookiejar.New(nil)
	client.Jar = jar

	uploadProtectedPresentation(t, client, ts.URL, token)

	resp, err := client.Get(ts.URL + "/notes")
	if err != nil {
		t.Fatalf("unauthenticated notes request: %v", err)
	}
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected redirect, got %s", resp.Status)
	}
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/login?returnTo=") {
		t.Fatalf("unexpected redirect location: %q", loc)
	}
	resp.Body.Close()

	loginForm := url.Values{}
	loginForm.Set("token", token)
	loginForm.Set("returnTo", "/notes")
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/login", strings.NewReader(loginForm.Encode()))
	if err != nil {
		t.Fatalf("new login request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("login request: %v", err)
	}
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected login redirect, got %s", resp.Status)
	}
	cookies := resp.Cookies()
	if len(cookies) == 0 || cookies[0].Name != browserAuthCookieName {
		t.Fatalf("expected auth cookie, got %#v", cookies)
	}
	if !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("unexpected cookie flags: %#v", cookies[0])
	}
	resp.Body.Close()

	for _, path := range []struct {
		path    string
		want    string
		message string
	}{
		{"/", "root", "root page"},
		{"/notes", "Active Notes Sessions", "notes dashboard"},
		{"/p/my-talk/", "Hello", "presentation"},
		{"/p/my-talk/app.js", "window.answer = 42;", "presentation asset"},
	} {
		resp, err = client.Get(ts.URL + path.path)
		if err != nil {
			t.Fatalf("%s request: %v", path.message, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %s", path.message, resp.Status)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.Contains(string(body), path.want) {
			t.Fatalf("%s body missing %q: %s", path.message, path.want, string(body))
		}
	}

	resp, err = client.Get(ts.URL + "/socket.io/?EIO=4&transport=polling&t=123")
	if err != nil {
		t.Fatalf("socket.io request: %v", err)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusFound {
		t.Fatalf("socket.io request should be authenticated, got %s", resp.Status)
	}
	resp.Body.Close()

	resp, err = client.Get(ts.URL + "/logout")
	if err != nil {
		t.Fatalf("logout request: %v", err)
	}
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected logout redirect, got %s", resp.Status)
	}
	resp.Body.Close()

	resp, err = client.Get(ts.URL + "/notes")
	if err != nil {
		t.Fatalf("post-logout notes request: %v", err)
	}
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected redirect after logout, got %s", resp.Status)
	}
	resp.Body.Close()
}

func TestBrowserAuthSetsSecureCookieOnHTTPS(t *testing.T) {
	ts, token := newAuthTestServer(t)
	client := noRedirectClient(ts)

	loginForm := url.Values{}
	loginForm.Set("token", token)
	loginForm.Set("returnTo", "/notes")
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/login", bytes.NewBufferString(loginForm.Encode()))
	if err != nil {
		t.Fatalf("new login request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Forwarded-Proto", "https")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("login request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected login redirect, got %s", resp.Status)
	}
	cookies := resp.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one cookie, got %#v", cookies)
	}
	cookie := cookies[0]
	if cookie.Name != browserAuthCookieName || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("unexpected cookie: %#v", cookie)
	}
}
