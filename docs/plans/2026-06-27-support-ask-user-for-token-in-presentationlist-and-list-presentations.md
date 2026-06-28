# Browser auth for protected presentations

## Context
- The server already supports a single `--accessToken`, but today it only protects the presentation management API via `Authorization: Bearer <token>`.
- Uploaded presentations at `/p/{name}/` and the notes UI remain readable without auth.
- Goal: if `--accessToken` is configured, browsers should be prompted once, then automatically reuse that authentication to open protected presentations over HTTPS, while the existing upload flow keeps working.

## Approach
- Keep bearer-token auth for the upload/list/delete API so `cmd/upload-presentation` and existing automation continue to work.
- Add a small auth layer in `internal/notes` that validates either `Authorization: Bearer <token>` or a server-issued browser session cookie derived from the same configured `--accessToken`.
- Prefer a form-based login plus signed/opaque `HttpOnly` cookie over Basic Auth or query tokens, so browsers can reuse auth automatically without exposing the token in URLs or JS.
- Protect all browser read surfaces selected in D1: `/`, `/p/{name}/...`, `/notes`, `/notes/{socketId}`, `/notes/sessions`, and the Socket.IO handshake path `/socket.io/`, while preserving current behavior when no token is configured.

## Files to modify
- `internal/notes/server.go`
- `internal/notes/handlers.go`
- `internal/notes/presentations.go`
- `internal/notes/auth.go` (new)
- `cmd/notes-server/main.go`
- `README.md`
- `internal/notes/presentations_test.go`
- `internal/notes/server_test.go`
- `internal/uploader/integration_test.go`

## Reuse
- `internal/notes/presentations.go`
  - `requireAccessToken(...)` already defines current bearer-token behavior for API endpoints and can be generalized or wrapped.
  - `HandleServePresentation(...)` is the current `/p/{name}/` file-serving entry point.
- `internal/notes/server.go`
  - Central route registration already cleanly separates API, uploaded presentations, notes pages, Socket.IO, `/health`, and `/`.
- `internal/notes/handlers.go`
  - `HandleDashboard(...)`, `HandleSpeakerView(...)`, `HandleSessionsJSON(...)`, and `HandleRoot(...)` are the browser read handlers that need auth wrapping under Option B.
- `internal/notes/index.html`
  - The speaker view loads `/socket.io/socket.io.js` and connects with `io.connect(window.location.origin)`, so a cookie-based session will automatically cover iframe and Socket.IO requests.
- `internal/uploader/client.go`
  - `BuildUploadRequest(...)` already sends `Authorization: Bearer ...` for uploads.
- `internal/uploader/integration_test.go`
  - Already exercises bearer-protected upload/list plus browser fetches of `/p/{name}/`; this is the best end-to-end auth regression suite.

## Decisions
### D1: Which browser routes should require the token when `--accessToken` is set?
- [x] Option A: Protect uploaded presentations (`/p/{name}/...`) and the uploaded-presentation listing flow only; leave `/`, `/notes`, and `/notes/sessions` open.
- [x] Option B: Protect all presentation-reading routes, including `/`, `/p/{name}/...`, `/notes`, `/notes/{socketId}`, and `/notes/sessions`, so the notes UI cannot bypass read protection.
- [x] C. Other: << user comment >>

## Steps
- [x] Add a small auth module/middleware layer that can:
  - accept bearer auth for API clients,
  - validate a browser login submission against the configured access token,
  - issue and verify an `HttpOnly` session cookie (`Secure` when HTTPS is used, `SameSite=Lax`, short/max-age bounded),
  - guard Socket.IO/browser requests with the same cookie without exposing the token to frontend JS.
- [x] Wire browser read-auth middleware in `internal/notes/server.go` onto `/`, `/p/{name}/...`, `/notes`, `/notes/{socketId}`, `/notes/sessions`, and `/socket.io/`, with `/health` remaining open and all auth disabled when `--accessToken` is empty.
- [x] Add minimal login/logout handlers/page in `internal/notes/handlers.go` that prompt for the token and redirect back to the originally requested page after success.
- [x] Keep upload/list/delete API compatibility by continuing to accept `Authorization: Bearer <token>` for machine clients and tests.
- [x] Update tests for:
  - unauthenticated browser access redirecting to login,
  - successful login producing a reusable cookie,
  - authenticated cookie access to protected HTML/assets and Socket.IO handshake requests,
  - existing uploader/bearer flows still passing.
- [x] Update README auth docs and examples to describe browser login, protected read routes, and unchanged bearer API usage.

## Verification
- Run `go test ./...`.
- Start the server without `--accessToken` and verify `/`, `/p/{name}/`, `/notes`, `/notes/sessions`, Socket.IO, and upload behavior are unchanged.
- Start the server with `--accessToken=secret` and verify:
  1. opening `/`, `/p/{name}/`, or `/notes` redirects to a login page,
  2. after successful login the browser can refresh and navigate protected presentation pages without re-entering the token,
  3. speaker view pages still load their embedded presentation iframes and connect to Socket.IO,
  4. `cmd/upload-presentation --access-token=secret` still uploads successfully,
  5. unauthenticated bearer/API requests still return `401`,
  6. logout clears the cookie and protected routes challenge again.
