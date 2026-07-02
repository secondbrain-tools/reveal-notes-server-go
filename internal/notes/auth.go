package notes

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zishang520/engine.io/utils"
)

const (
	browserAuthCookieName = "remote_notes_session"
	browserAuthCookieTTL  = 8 * time.Hour

	authThrottleFailureLimit  = 5
	authThrottleFailureWindow = 5 * time.Minute
	authThrottleLockout       = 15 * time.Minute
)

type authAttemptThrottle struct {
	mu       sync.Mutex
	attempts map[string]*authAttemptState
}

type authAttemptState struct {
	failures     int
	firstFailure time.Time
	blockedUntil time.Time
}

func newAuthAttemptThrottle() *authAttemptThrottle {
	return &authAttemptThrottle{attempts: make(map[string]*authAttemptState)}
}

func (t *authAttemptThrottle) clientKey(r *http.Request) string {
	if r == nil {
		return ""
	}
	if host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr)); err == nil && host != "" {
		return host
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "<unknown>"
}

func (t *authAttemptThrottle) limited(r *http.Request) bool {
	if t == nil {
		return false
	}
	key := t.clientKey(r)
	now := now().UTC()

	t.mu.Lock()
	defer t.mu.Unlock()

	st := t.attempts[key]
	if st == nil {
		return false
	}
	if !st.blockedUntil.IsZero() && now.Before(st.blockedUntil) {
		return true
	}
	if !st.blockedUntil.IsZero() && !now.Before(st.blockedUntil) {
		st.blockedUntil = time.Time{}
		st.failures = 0
		st.firstFailure = now
	}
	if !st.firstFailure.IsZero() && now.Sub(st.firstFailure) > authThrottleFailureWindow {
		delete(t.attempts, key)
		return false
	}
	return false
}

func (t *authAttemptThrottle) recordFailure(r *http.Request) bool {
	if t == nil {
		return false
	}
	key := t.clientKey(r)
	now := now().UTC()

	t.mu.Lock()
	defer t.mu.Unlock()

	st := t.attempts[key]
	if st == nil {
		st = &authAttemptState{firstFailure: now}
		t.attempts[key] = st
	}
	if !st.blockedUntil.IsZero() && now.Before(st.blockedUntil) {
		return true
	}
	if st.blockedUntil.IsZero() && !st.firstFailure.IsZero() && now.Sub(st.firstFailure) > authThrottleFailureWindow {
		st.failures = 0
		st.firstFailure = now
	}
	if st.blockedUntil != (time.Time{}) && !now.Before(st.blockedUntil) {
		st.blockedUntil = time.Time{}
		st.failures = 0
		st.firstFailure = now
	}
	if st.firstFailure.IsZero() {
		st.firstFailure = now
	}
	st.failures++
	if st.failures > authThrottleFailureLimit {
		st.failures = 0
		st.firstFailure = now
		st.blockedUntil = now.Add(authThrottleLockout)
		return true
	}
	return false
}

func (t *authAttemptThrottle) reset(r *http.Request) {
	if t == nil {
		return
	}
	key := t.clientKey(r)
	t.mu.Lock()
	delete(t.attempts, key)
	t.mu.Unlock()
}

type browserAuth struct {
	token    string
	throttle *authAttemptThrottle
}

func newBrowserAuth(token string) *browserAuth {
	return &browserAuth{token: token, throttle: newAuthAttemptThrottle()}
}

func (a *browserAuth) enabled() bool {
	return a != nil && a.token != ""
}

func (a *browserAuth) authThrottled(r *http.Request) bool {
	return a != nil && a.enabled() && a.throttle != nil && a.throttle.limited(r)
}

func (a *browserAuth) recordAuthFailure(r *http.Request) bool {
	if a == nil || !a.enabled() || a.throttle == nil {
		return false
	}
	return a.throttle.recordFailure(r)
}

func (a *browserAuth) recordAuthSuccess(r *http.Request) {
	if a == nil || !a.enabled() || a.throttle == nil {
		return
	}
	a.throttle.reset(r)
}

func requestAuthChannels(r *http.Request) string {
	if r == nil {
		return "<nil>"
	}
	sources := make([]string, 0, 3)
	if r.Header.Get("Authorization") != "" {
		sources = append(sources, "bearer")
	}
	if r.Header.Get("Cookie") != "" {
		sources = append(sources, "cookie")
	}
	if r.URL != nil {
		if _, ok := r.URL.Query()["token"]; ok {
			sources = append(sources, "query-token")
		}
	}
	if len(sources) == 0 {
		return "none"
	}
	return strings.Join(sources, ",")
}

func handshakePresenceSummary(authPayload map[string]any, query *utils.ParameterBag) string {
	parts := []string{"auth_payload=false", "query_token=false"}
	if authPayload != nil {
		parts[0] = "auth_payload=true"
		if auth, ok := authPayload["auth"].(map[string]any); ok {
			if _, ok := auth["token"]; ok {
				parts = append(parts, "auth_token=true")
			}
		}
		if _, ok := authPayload["token"]; ok {
			parts = append(parts, "auth_token=true")
		}
	}
	if query != nil {
		if v, _ := query.Get("token"); v != "" {
			parts[1] = "query_token=true"
		}
	}
	return strings.Join(parts, " ")
}
func hasBearerToken(r *http.Request, token string) bool {
	return subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) == 1
}

func validateHandshakeToken(expected, provided string) bool {
	if expected == "" {
		return true
	}
	if provided == "" || len(provided) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

// hasQueryToken reports whether the request carries the access token as
// ?token=... in its query string. This is how the notes-client JS plugin
// forwards the token via Socket.IO's query channel on cross-origin
// polling / WebSocket upgrades.
func hasQueryToken(r *http.Request, token string) bool {
	return validateHandshakeToken(token, r.URL.Query().Get("token"))
}

// authorizeHandshake validates a Socket.IO handshake against the configured
// access token. It checks the auth payload first, then the query string,
// returning a non-nil error if both fail.
//
// An empty `expected` token authorizes every handshake (open mode).
func authorizeHandshake(expected string, authPayload map[string]any, query *utils.ParameterBag) error {
	if expected == "" {
		log.Printf("[auth] handshake: open mode allowed (%s)", handshakePresenceSummary(authPayload, query))
		return nil
	}
	if authPayload != nil {
		if auth, ok := authPayload["auth"].(map[string]any); ok {
			if token, _ := auth["token"].(string); validateHandshakeToken(expected, token) {
				log.Printf("[auth] handshake: accepted via auth.payload (%s)", handshakePresenceSummary(authPayload, query))
				return nil
			}
		}
		if token, _ := authPayload["token"].(string); validateHandshakeToken(expected, token) {
			log.Printf("[auth] handshake: accepted via auth.token (%s)", handshakePresenceSummary(authPayload, query))
			return nil
		}
	}
	if query != nil {
		if v, _ := query.Get("token"); validateHandshakeToken(expected, v) {
			log.Printf("[auth] handshake: accepted via query.token (%s)", handshakePresenceSummary(authPayload, query))
			return nil
		}
	}
	log.Printf("[auth] handshake: rejected (%s)", handshakePresenceSummary(authPayload, query))
	return errors.New("unauthorized")
}

// authenticatedViaCookieHeader parses a raw Cookie header value and
// returns true if it contains a valid HMAC-signed session cookie.
// Used by Socket.IO middleware as a fallback when Firefox strips
// cookies from the JS-visible API on WebSocket upgrades.
func authenticatedViaCookieHeader(cookieHeader, token string) bool {
	if token == "" {
		return true
	}
	for _, part := range strings.Split(cookieHeader, ";") {
		part = strings.TrimSpace(part)
		eq := strings.IndexByte(part, '=')
		if eq <= 0 {
			continue
		}
		name, val := part[:eq], part[eq+1:]
		if name == browserAuthCookieName && validSessionCookieValue(val, token) {
			return true
		}
	}
	return false
}

// validSessionCookieValue verifies that `value` is a well-formed,
// non-expired HMAC-signed session cookie. Used as a fallback in
// Socket.IO middleware so a `query.token` carrying the cookie value
// (forwarded by the speaker view's JS as a Firefox belt-and-braces)
// is accepted instead of the configured access token.
func validSessionCookieValue(value, token string) bool {
	if token == "" {
		return true
	}
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
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte(parts[0]))
	expected := hex.EncodeToString(mac.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(parts[1]), []byte(expected)) == 1
}

func (a *browserAuth) authenticated(r *http.Request) bool {
	if !a.enabled() {
		return true
	}
	if hasBearerToken(r, a.token) {
		return true
	}
	if hasQueryToken(r, a.token) {
		return true
	}
	// Belt-and-braces: the speaker view JS may forward the cookie VALUE
	// as ?token= when Firefox strips the cookie on WebSocket upgrades.
	// The cookie value is itself a valid HMAC-signed assertion, so
	// accepting it here is symmetric with accepting it from the
	// Cookie header below.
	if t := r.URL.Query().Get("token"); t != "" && validSessionCookieValue(t, a.token) {
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
			a.recordAuthSuccess(r)
			// If auth succeeded via a one-time ?token=... query param,
			// promote it to a signed session cookie. We do NOT redirect
			// to a clean URL — the browser would drop ?token= from the
			// URL on redirect, and the speaker view's own JS needs to
			// capture it from the URL in order to forward it on the
			// Socket.IO connection. The JS strips the token via
			// history.replaceState after the Socket.IO connect is in
			// flight.
			if t := r.URL.Query().Get("token"); t != "" && hasQueryToken(r, a.token) {
				a.setSessionCookie(w, r)
				log.Printf("[auth] wrapPage: served with cookie, query token present (JS will strip)")
				next.ServeHTTP(w, r)
				return
			}
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
		setCORSHeaders(w, r)
		// Handle CORS preflight — no auth required for OPTIONS.
		if r.Method == http.MethodOptions {
			log.Printf("[auth] wrapSocket: method=%s path=%s origin=%q auth=%s result=preflight", r.Method, r.URL.Path, r.Header.Get("Origin"), requestAuthChannels(r))
			w.WriteHeader(http.StatusNoContent)
			return
		}
		authSummary := requestAuthChannels(r)
		if a.authThrottled(r) {
			log.Printf("[auth] wrapSocket: method=%s path=%s origin=%q auth=%s result=throttled", r.Method, r.URL.Path, r.Header.Get("Origin"), authSummary)
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		if a.authenticated(r) {
			a.recordAuthSuccess(r)
			log.Printf("[auth] wrapSocket: method=%s path=%s origin=%q auth=%s result=accepted", r.Method, r.URL.Path, r.Header.Get("Origin"), authSummary)
			next.ServeHTTP(w, r)
			return
		}
		if a.recordAuthFailure(r) {
			log.Printf("[auth] wrapSocket: method=%s path=%s origin=%q auth=%s result=throttled", r.Method, r.URL.Path, r.Header.Get("Origin"), authSummary)
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		log.Printf("[auth] wrapSocket: method=%s path=%s origin=%q auth=%s result=rejected", r.Method, r.URL.Path, r.Header.Get("Origin"), authSummary)
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
			returnTo := cleanLoginReturnTo(r.URL.Query().Get("returnTo"))
			if a.authenticated(r) {
				a.recordAuthSuccess(r)
				http.Redirect(w, r, returnTo, http.StatusSeeOther)
				return
			}
			renderLoginPage(w, returnTo, "", http.StatusOK)
		case http.MethodPost:
			if err := r.ParseForm(); err != nil {
				renderLoginPage(w, cleanLoginReturnTo("/"), "invalid login form", http.StatusBadRequest)
				return
			}
			returnTo := cleanLoginReturnTo(r.FormValue("returnTo"))
			if a.authThrottled(r) {
				renderLoginPage(w, returnTo, "too many failed attempts", http.StatusTooManyRequests)
				return
			}
			provided := r.FormValue("token")
			if subtle.ConstantTimeCompare([]byte(provided), []byte(a.token)) != 1 {
				status := http.StatusUnauthorized
				if a.recordAuthFailure(r) {
					status = http.StatusTooManyRequests
				}
				renderLoginPage(w, returnTo, "invalid access token", status)
				return
			}
			a.recordAuthSuccess(r)
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

func cleanLoginReturnTo(raw string) string {
	target := cleanReturnTo(raw)
	parsed, err := url.Parse(target)
	if err != nil {
		return target
	}
	query := parsed.Query()
	if _, ok := query["token"]; !ok {
		return target
	}
	query.Del("token")
	parsed.RawQuery = query.Encode()
	return parsed.String()
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
	http.Redirect(w, r, "/login?returnTo="+url.QueryEscape(cleanLoginReturnTo(r.URL.RequestURI())), http.StatusFound)
}

// tokenExchangeHandler accepts the access token from a cross-origin POST
// and, if it matches, sets the browser session cookie. The publisher's
// presentation page uses this to hand off the token once, so the
// speaker-notes page can authenticate via the cookie instead of having the
// token persist in the URL.
//
// Mounted outside auth.wrapPage so OPTIONS preflight bypasses the
// /login redirect — the browser rejects any 3xx on a preflight.
func (a *browserAuth) tokenExchangeHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		log.Printf("[auth] /auth-token: method=%s origin=%q open_mode=%v", r.Method, origin, !a.enabled())
		// Always answer CORS preflight cleanly, regardless of auth state.
		// This must happen BEFORE any auth check or writeHeader call so
		// the browser's preflight sees our 204 (not a redirect).
			setCORSHeaders(w, r)
		if r.Method == http.MethodOptions || r.Method == http.MethodHead {
			log.Printf("[auth] /auth-token: preflight → 204")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			log.Printf("[auth] /auth-token: not POST → 405")
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if a.authThrottled(r) {
			log.Printf("[auth] /auth-token: throttled")
			http.Error(w, `{"error":"too many requests"}`, http.StatusTooManyRequests)
			return
		}

		// Open mode (no --accessToken configured) — set a no-op session
		// cookie so the speaker-notes page stops redirecting through /login.
		if !a.enabled() {
			a.recordAuthSuccess(r)
			a.setSessionCookie(w, r)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true,"open":true}`))
			return
		}

		// Accept the token from either a JSON body or a form field.
		var provided string
		if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			var payload struct {
				Token string `json:"token"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err == nil {
				provided = payload.Token
			}
		} else if err := r.ParseForm(); err == nil {
			provided = r.FormValue("token")
		}

		if provided == "" {
			status := http.StatusBadRequest
			if a.recordAuthFailure(r) {
				status = http.StatusTooManyRequests
			}
			http.Error(w, `{"error":"missing token"}`, status)
			return
		}
		if subtle.ConstantTimeCompare([]byte(provided), []byte(a.token)) != 1 {
			status := http.StatusUnauthorized
			if a.recordAuthFailure(r) {
				status = http.StatusTooManyRequests
			}
			http.Error(w, `{"error":"invalid token"}`, status)
			return
		}

		// Promote the supplied token into a signed session cookie so
		// subsequent same-origin navigations authenticate via the cookie.
		a.recordAuthSuccess(r)
		a.setSessionCookie(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}
}

// setCORSHeaders mirrors the request Origin and enables credentialed
// cross-origin requests for every service route.
func setCORSHeaders(w http.ResponseWriter, r *http.Request) {
	if r == nil {
		return
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	if acrh := r.Header.Get("Access-Control-Request-Headers"); acrh != "" {
		w.Header().Set("Access-Control-Allow-Headers", acrh)
	} else {
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept, Origin, X-Requested-With")
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
	w.Header().Set("Vary", "Origin")
}

// withCORS adds permissive cross-origin headers to every response and
// short-circuits OPTIONS preflight requests before auth handlers run.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
