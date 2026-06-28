package notes

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	browserAuthCookieName = "remote_notes_session"
	browserAuthCookieTTL  = 8 * time.Hour
)

type browserAuth struct {
	token string
}

func newBrowserAuth(token string) *browserAuth {
	return &browserAuth{token: token}
}

func (a *browserAuth) enabled() bool {
	return a != nil && a.token != ""
}

func hasBearerToken(r *http.Request, token string) bool {
	return subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) == 1
}

func (a *browserAuth) authenticated(r *http.Request) bool {
	if !a.enabled() {
		return true
	}
	if hasBearerToken(r, a.token) {
		return true
	}
	cookie, err := r.Cookie(browserAuthCookieName)
	if err != nil {
		return false
	}
	return a.validSessionCookie(cookie.Value)
}

func (a *browserAuth) wrapPage(next http.Handler) http.HandlerFunc {
	if !a.enabled() {
		return next.ServeHTTP
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if a.authenticated(r) {
			next.ServeHTTP(w, r)
			return
		}
		a.redirectToLogin(w, r)
	}
}

func (a *browserAuth) wrapSocket(next http.Handler) http.HandlerFunc {
	if !a.enabled() {
		return next.ServeHTTP
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if a.authenticated(r) {
			next.ServeHTTP(w, r)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}
}

func (a *browserAuth) loginHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.enabled() {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}

		switch r.Method {
		case http.MethodGet:
			returnTo := cleanReturnTo(r.URL.Query().Get("returnTo"))
			if a.authenticated(r) {
				http.Redirect(w, r, returnTo, http.StatusSeeOther)
				return
			}
			renderLoginPage(w, returnTo, "", http.StatusOK)
		case http.MethodPost:
			if err := r.ParseForm(); err != nil {
				renderLoginPage(w, "/", "invalid login form", http.StatusBadRequest)
				return
			}
			returnTo := cleanReturnTo(r.FormValue("returnTo"))
			if subtle.ConstantTimeCompare([]byte(r.FormValue("token")), []byte(a.token)) != 1 {
				renderLoginPage(w, returnTo, "invalid access token", http.StatusUnauthorized)
				return
			}
			a.setSessionCookie(w, r)
			http.Redirect(w, r, returnTo, http.StatusSeeOther)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func (a *browserAuth) logoutHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.enabled() {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		a.clearSessionCookie(w, r)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	}
}

func (a *browserAuth) setSessionCookie(w http.ResponseWriter, r *http.Request) {
	expires := now().Add(browserAuthCookieTTL).UTC()
	cookie := &http.Cookie{
		Name:     browserAuthCookieName,
		Value:    a.signSessionCookie(expires.Unix()),
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(browserAuthCookieTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPSRequest(r),
	}
	http.SetCookie(w, cookie)
}

func (a *browserAuth) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     browserAuthCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0).UTC(),
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPSRequest(r),
	})
}

func (a *browserAuth) signSessionCookie(expiresUnix int64) string {
	mac := hmac.New(sha256.New, []byte(a.token))
	_, _ = mac.Write([]byte(strconv.FormatInt(expiresUnix, 10)))
	return fmt.Sprintf("%d.%s", expiresUnix, hex.EncodeToString(mac.Sum(nil)))
}

func (a *browserAuth) validSessionCookie(value string) bool {
	parts := strings.SplitN(value, ".", 2)
	if len(parts) != 2 {
		return false
	}
	expiresUnix, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return false
	}
	if now().UTC().Unix() > expiresUnix {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(value), []byte(a.signSessionCookie(expiresUnix))) == 1
}

func isHTTPSRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func cleanReturnTo(raw string) string {
	if raw == "" {
		return "/"
	}
	if strings.HasPrefix(raw, "//") {
		return "/"
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" {
		return "/"
	}
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	if !strings.HasPrefix(parsed.Path, "/") {
		return "/"
	}
	return (&url.URL{Path: parsed.Path, RawQuery: parsed.RawQuery, Fragment: parsed.Fragment}).String()
}

var loginPageTemplate = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>Sign in</title>
  <style>
    body { margin: 0; font-family: system-ui, -apple-system, sans-serif; background: #f6f7fb; color: #112131; }
    main { min-height: 100vh; display: grid; place-items: center; padding: 1rem; }
    form { width: min(420px, 100%); background: #fff; border: 1px solid #d6dde6; border-radius: 14px; padding: 1.2rem; box-shadow: 0 10px 20px rgba(16,40,70,.08); display: grid; gap: .85rem; }
    h1 { margin: 0; font-size: 1.4rem; }
    p { margin: 0; color: #586779; }
    label { display: grid; gap: .35rem; font-weight: 600; }
    input { font: inherit; padding: .75rem .85rem; border: 1px solid #cbd5e1; border-radius: 10px; }
    button { font: inherit; border: 0; border-radius: 10px; padding: .8rem 1rem; background: #0050b8; color: #fff; font-weight: 700; cursor: pointer; }
    .error { color: #b42318; }
    .hint { font-size: .9rem; }
  </style>
</head>
<body>
  <main>
    <form method="post" action="/login">
      <h1>Sign in</h1>
      <p>Enter the access token to open presentations.</p>
      {{if .Error}}<p class="error">{{.Error}}</p>{{end}}
      <label>
        Access token
        <input type="password" name="token" autocomplete="current-password" autofocus spellcheck="false" />
      </label>
      <input type="hidden" name="returnTo" value="{{.ReturnTo}}" />
      <button type="submit">Continue</button>
      <p class="hint">Your session is stored in a secure, HttpOnly cookie.</p>
    </form>
  </main>
</body>
</html>`))

type loginPageData struct {
	ReturnTo string
	Error    string
}

func renderLoginPage(w http.ResponseWriter, returnTo, errMsg string, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = loginPageTemplate.Execute(w, loginPageData{ReturnTo: returnTo, Error: errMsg})
}

func (a *browserAuth) redirectToLogin(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/login?returnTo="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
}
