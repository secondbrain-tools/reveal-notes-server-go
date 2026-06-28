# Upload tool

## Context
- This repo already has a Go server-side upload API at `POST /api/presentations/{name}` plus list/delete/serve endpoints for uploaded presentations.
- Bearer-token support is already implemented on the server for write/list endpoints via `requireAccessToken(...)`; if `--accessToken` is set, callers must send `Authorization: Bearer <token>`.
- What is missing is a client upload tool that packages a presentation directory, forces the chosen HTML file to become `index.html` inside the archive, applies ignore filters, and submits multipart form field `file` to the existing API.

## Approach
- Recommended approach: add a second Go CLI entrypoint such as `cmd/upload-presentation/main.go`, keeping the uploader in the same module/toolchain as the server.
- Put the packaging + HTTP client logic in a reusable internal package (likely `internal/notes` if we want to share name validation directly, or a small new helper package if that keeps tests cleaner). The important part is that `cmd/upload-presentation` stays thin and most behavior is unit-testable without spawning processes.
- Because the user selected gitignore-style ignores, plan to support repeated `--ignore` patterns using gitignore semantics rather than ad-hoc extension/name rules; if stdlib matching is too limited, add a small matcher dependency and cover it with tests.
- Reuse the server's presentation-name rule by exposing a shared helper from `internal/notes` instead of duplicating the regex in the CLI.
- Keep the server API contract intact: the existing route already accepts multipart form field `file`, and `server.go` already wraps upload/list/delete routes with bearer-token middleware.
- Add one true integration test with `httptest.NewServer(server.Mux)` that uploads through HTTP, verifies `201 Created`, then fetches `/p/{name}/` and `/api/presentations` with auth.
## Files to modify
- `cmd/upload-presentation/main.go` — new upload CLI entrypoint and flag parsing.
- `internal/notes/presentations.go` — expose shared presentation-name validation and only touch server upload code if tests reveal a small gap.
- `internal/notes/presentations_test.go` — extend handler/auth coverage and add request-construction/unit helpers if the uploader logic lands in `internal/notes`.
- `internal/notes/upload_integration_test.go` — new end-to-end HTTP test using `httptest.NewServer`.
- `README.md` — document the uploader CLI, bearer-token usage, and ignore-pattern behavior.
- `Makefile` — add a discoverable target for building the uploader binary.
- `go.mod` / `go.sum` — only if gitignore-style ignores need a matcher dependency.
- `manifest.yaml` — probably no change unless we explicitly want the Publisher runtime to build/distribute the uploader binary too.
## Reuse
- `internal/notes/presentations.go`
  - `requireAccessToken(...)` already enforces exact `Authorization: Bearer <token>` matching.
  - `HandleUploadPresentation(...)` already accepts multipart field `file`, validates names, and stores uploaded zips.
  - `validPresentationName` already defines the slug constraint to reuse via a shared exported helper.
- `internal/notes/server.go`
  - already wires `POST/PUT /api/presentations/{name}`, `GET /api/presentations`, and `DELETE /api/presentations/{name}` behind bearer auth.
  - already serves uploaded files at `/p/{name}/` without auth, which is what the integration test should verify after upload.
- `internal/notes/presentations_test.go`
  - `createZip(...)` and `createMultipartBody(...)` are existing test helpers that can be reused/expanded.
  - current tests already cover basic upload replacement and invalid-name rejection.
- `internal/notes/server_test.go` — already has `NewServer(...)` setup patterns, but does not yet do a real `httptest.NewServer` upload/fetch flow.
- `README.md` — already documents the raw curl upload/auth workflow, so the new CLI docs should mirror that contract.
- `manifest.yaml` — currently only builds `./cmd/notes-server`, which confirms uploader distribution is still a product decision rather than an existing pattern.
- Verified current baseline: `go test ./...` passes before this change.
## Steps
- [x] Add `cmd/upload-presentation/main.go` with flags for server URL, presentation name, source folder, HTML file path, optional bearer token, and repeated `--ignore` patterns. [DONE:1]
- [x] Expose a shared presentation-name validation helper from `internal/notes` so the CLI fails invalid slugs before any network request. [DONE:2]
- [x] Implement packaging logic that walks the source folder, applies gitignore-style ignore matching, rewrites the selected HTML file to zip entry `index.html`, preserves all other included relative paths, and rejects invalid/missing/out-of-tree input early. [DONE:3]
- [x] Implement multipart upload to `POST /api/presentations/{name}` with form field `file` and optional `Authorization: Bearer ...`. [DONE:4]
- [x] Add focused unit tests for ignore matching, selected HTML renaming, out-of-tree/duplicate-path validation, and request/auth construction. [DONE:5]
- [x] Add an integration test using `httptest.NewServer` that uploads a generated presentation to `notes.NewServer(...)`, verifies the protected API with bearer auth, and fetches `/p/{name}/` to confirm the uploaded site is served. [DONE:6]
- [x] Update `README.md` and `Makefile`; leave `manifest.yaml` unchanged unless the decision below says the uploader should ship as part of the runtime artifact. [DONE:7]
## Verification
- Run `go test ./...`.
- Add/execute a focused integration test that:
  1. starts `notes.NewServer(...)` behind `httptest.NewServer` with `AccessToken` set,
  2. creates a temporary presentation folder containing an HTML entry file plus ignored/non-ignored assets,
  3. runs the uploader logic against `POST /api/presentations/{name}` with bearer auth,
  4. verifies `201 Created`,
  5. verifies `GET /api/presentations` succeeds with auth,
  6. fetches `/p/{name}/` and confirms the selected HTML is now served as `index.html`,
  7. confirms ignored files are absent from the served/uploaded output,
  8. verifies missing/wrong auth still returns `401` on protected endpoints.
- Manual smoke test: build the uploader binary, upload a local exported presentation, open `http://host:1947/p/{name}/`, and verify the result matches the original exported deck except for the entry file rename to `index.html`.

### D1: Ignore filter syntax

- [x] Option A: Simple repeated `--ignore` values interpreted as path prefixes / exact names / extensions by lightweight rules documented in the CLI. [DONE:8]
- [x] Option B: Gitignore-style pattern matching for `--ignore` values. [DONE:9]
- [x] C. Other: << user comment >> [DONE:10]

### D2: Uploader distribution scope

- [x] Option A: Keep the uploader as a developer/local CLI only — document it in `README.md` and add `Makefile` build targets, but do not change `manifest.yaml`. [DONE:11]
- [x] Option B: Also update `manifest.yaml` / packaging so the uploader binary is built or distributed alongside the runtime. [DONE:12]
- [x] C. Other: << user comment >> [DONE:13]
