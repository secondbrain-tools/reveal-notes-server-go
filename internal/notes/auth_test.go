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

	"github.com/zishang520/engine.io/utils"
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

// newAuthTestServerWithToken creates a test server with a configured access
// token. The presentation index file is added so the mux handles the root
// route. It reuses the shared testServer type from server_test.go.
func newAuthTestServerWithToken(t *testing.T, token string) *testServer {
	t.Helper()
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "index.html"), []byte("<html></html>"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := ServerConfig{
		Hostname:          "127.0.0.1",
		Port:              0,
		PresentationDir:   tmpDir,
		PresentationIndex: "/index.html",
		ActiveTtlMs:       7200000,
		AccessToken:       token,
	}
	s := NewServer(cfg)
	return &testServer{server: s, mux: s.Mux}
}

func TestHasQueryToken(t *testing.T) {
	cases := []struct {
		name     string
		url      string
		expected string
		want     bool
	}{
		{"empty expected allows any token (open mode)", "http://x/?token=abc", "", true},
		{"matching token", "http://x/?token=secret", "secret", true},
		{"wrong token", "http://x/?token=other", "secret", false},
		{"missing query", "http://x/", "secret", false},
		{"empty query value", "http://x/?token=", "secret", false},
		{"token in unrelated param ignored", "http://x/?t=secret", "secret", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tc.url, nil)
			if got := hasQueryToken(r, tc.expected); got != tc.want {
				t.Fatalf("hasQueryToken(%q, %q) = %v, want %v", tc.url, tc.expected, got, tc.want)
			}
		})
	}
}


func TestValidateHandshakeToken(t *testing.T) {
	if !validateHandshakeToken("", "anything") {
		t.Fatal("empty expected must authorize every provided token")
	}
	if validateHandshakeToken("secret", "") {
		t.Fatal("non-empty expected must reject empty provided")
	}
	if !validateHandshakeToken("secret", "secret") {
		t.Fatal("matching tokens must validate")
	}
	if validateHandshakeToken("secret", "Secret") {
		t.Fatal("tokens must match exactly (no case folding)")
	}
	if validateHandshakeToken("secret", "secre") {
		t.Fatal("prefix must not validate")
	}
}

func TestAuthorizeHandshake(t *testing.T) {
	q := utils.NewParameterBag(map[string][]string{"token": {"secret"}})
	auth := map[string]any{"auth": map[string]any{"token": "secret"}}
	wrong := map[string]any{"auth": map[string]any{"token": "nope"}}

	// No token configured: every handshake is authorized.
	if err := authorizeHandshake("", auth, q); err != nil {
		t.Fatalf("expected no error when no token is configured, got %v", err)
	}
	if err := authorizeHandshake("", nil, nil); err != nil {
		t.Fatalf("expected no error for nil handshake with no token configured, got %v", err)
	}

	// Token configured, handshake matches via auth payload.
	if err := authorizeHandshake("secret", auth, q); err != nil {
		t.Fatalf("expected no error for matching auth payload, got %v", err)
	}

	// Token configured, handshake matches via query string.
	if err := authorizeHandshake("secret", nil, q); err != nil {
		t.Fatalf("expected no error for matching query string, got %v", err)
	}

	// Token configured, BOTH auth payload and query are wrong — must reject.
	wrongQ := utils.NewParameterBag(map[string][]string{"token": {"nope"}})
	if err := authorizeHandshake("secret", wrong, wrongQ); err == nil {
		t.Fatal("expected error when both auth and query tokens are wrong")
	} else if err.Error() != "unauthorized" {
		t.Fatalf("expected 'unauthorized' error, got %q", err.Error())
	}

	// Token configured, handshake is missing entirely.
	if err := authorizeHandshake("secret", nil, nil); err == nil {
		t.Fatal("expected error for missing token")
	}
	// Token configured, query has wrong value AND auth payload has wrong
	// value. Either one being correct is enough (query takes precedence
	// as the canonical channel).
	if err := authorizeHandshake("secret", wrong, q); err != nil {
		t.Fatalf("query.token alone should authorize (auth payload is optional), got %v", err)
	}
}

func TestWrapSocketAcceptsQueryStringToken(t *testing.T) {
	ts := newAuthTestServerWithToken(t, "secret")

	// Missing token and no cookie: blocked at the edge.
	req := httptest.NewRequest(http.MethodGet, "/socket.io/?EIO=4&transport=polling", nil)
	if resp := ts.Do(req); resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", resp.Code)
	}

	// Wrong query token: blocked at the edge.
	req = httptest.NewRequest(http.MethodGet, "/socket.io/?EIO=4&transport=polling&token=wrong", nil)
	if resp := ts.Do(req); resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong query token, got %d", resp.Code)
	}

	// Correct query token: the engine.io handler is reached. It still
	// needs the EIO/transport params set; with polling transport it
	// responds 200 with the open payload.
	req = httptest.NewRequest(http.MethodGet, "/socket.io/?EIO=4&transport=polling&token=secret&t=test", nil)
	if resp := ts.Do(req); resp.Code != http.StatusOK {
		t.Fatalf("expected 200 with correct query token, got %d (body=%s)", resp.Code, resp.Body.String())
	}
}

func TestWrapSocketQueryTokenDisabledWhenNoAccessToken(t *testing.T) {
	// When no access token is configured, the auth middleware is a no-op
	// and any request — including ones with a stray ?token=... — must pass.
	ts := newAuthTestServerWithToken(t, "")

	req := httptest.NewRequest(http.MethodGet, "/socket.io/?EIO=4&transport=polling&t=test", nil)
	if resp := ts.Do(req); resp.Code != http.StatusOK {
		t.Fatalf("expected 200 when auth is disabled, got %d", resp.Code)
	}
}
