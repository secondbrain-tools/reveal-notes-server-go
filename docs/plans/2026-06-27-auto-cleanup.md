# Auto Cleanup

## Context
- Uploaded presentations are documented as auto-deleted after `--presentationTtlMs`, but the current cleanup is only in-memory.
- `internal/notes/presentations.go` tracks uploaded presentations in `PresentationStore.items`, and `Prune()` only removes entries that were added during the current process lifetime.
- `internal/notes/handlers.go` serves `/p/{name}/...` directly from disk, so presentation directories left behind after a restart remain accessible even when they are no longer tracked by the store.
- `manifest.yaml` starts the runtime with `--idleShutdownMs=15000`, so the server is expected to exit shortly after clients disconnect; that makes restart-safe cleanup important because stale presentation directories can accumulate across runs.

## Approach
- Make uploaded-presentation cleanup durable across restarts by persisting enough metadata to reconstruct the presentation store from disk on startup.
- Keep the existing `PresentationStore` API shape (`Add`, `List`, `Remove`, `Prune`) and extend its implementation so startup indexing and pruning operate on persisted presentation metadata instead of process-local memory only.
- Store upload metadata alongside each extracted presentation directory (for example created time and stored size), load valid metadata files during store initialization, and ignore/remove expired entries during startup and periodic pruning.
- Keep serving uploaded presentations from disk at `/p/{name}/...`, but ensure cleanup decisions come from the persisted metadata so old directories are actually removed.

## Files to modify
- `internal/notes/presentations.go` — persist presentation metadata, load existing presentations from disk, and make prune/remove logic restart-safe.
- `internal/notes/server.go` — ensure startup initialization/pruning is triggered when the server creates the presentation store.
- `internal/notes/presentations_test.go` — add coverage for restart-safe loading, pruning, and metadata handling.
- `internal/notes/server_test.go` — add or extend server-level coverage if startup behavior is exercised via `NewServer(...)`.
- `README.md` — tighten the auto-cleanup documentation so it matches the restart-safe behavior.

## Reuse
- `internal/notes/presentations.go`
  - `PresentationStore.Add(...)` already owns upload extraction and is the right place to write persisted metadata.
  - `PresentationStore.Remove(...)` already centralizes disk deletion.
  - `PresentationStore.Prune()` already expresses the intended TTL cleanup behavior.
- `internal/notes/presentations.go`
  - `HandleListPresentations(...)`, `HandleDeletePresentation(...)`, and `HandleServePresentation(...)` already define the API/serving contract that the cleanup change must preserve.
- `internal/notes/server.go`
  - `NewServer(...)` already creates the presentation store and starts the periodic prune ticker, so startup reload behavior should be covered either through `NewPresentationStore(...)` directly or one server-level test.
- `internal/notes/handlers.go`
  - `HandleSpeakerView(...)` already falls back to `/` when an uploaded presentation directory is gone, so cleanup tests can verify stale directories stop being used without changing routing behavior.
- `internal/notes/presentations_test.go`
  - Existing upload/list/delete/prune tests already cover the current store behavior and can be extended with restart/load-from-disk cases instead of building a new test harness.
- `internal/notes/server_test.go`
  - Existing `NewServer(...)` and mux-based tests can host one end-to-end restart/404 assertion if store initialization needs server-level coverage.
- `manifest.yaml`
  - Current `--idleShutdownMs=15000` runtime configuration explains why durable cleanup matters; likely no manifest change is needed.

## Steps
- [x] 1. Add persisted presentation metadata writing during upload/replace so each extracted presentation has stable cleanup/listing data after process restart.
- [x] 2. Teach `NewPresentationStore(...)` to scan the presentations directory, rebuild `items`, and drop invalid/expired entries on startup.
- [x] 3. Update prune/remove paths so in-memory state and on-disk directories stay consistent, including stale directories discovered from disk.
- [x] 4. Extend tests for restart scenarios: upload, recreate store/server, verify list output, verify TTL pruning removes expired directories, and verify stale directories do not remain servable after cleanup.
- [x] 5. Update `README.md` wording/examples so auto-cleanup behavior is accurate.

## Verification
- Run `go test ./...`.
- Add/execute automated restart-focused tests in `internal/notes/presentations_test.go` for:
  1. upload a presentation, recreate `PresentationStore`, and verify `List()` still returns the uploaded item with persisted `createdAt`/`size` metadata,
  2. upload a replacement for the same name and verify the metadata file is rewritten alongside the extracted directory,
  3. create expired or malformed on-disk presentation entries before store construction and verify startup load prunes them from both `items` and disk,
  4. advance the mocked clock past TTL, run `Prune()`, and verify the presentation disappears from `List()` and its directory is removed from disk.
- Add/execute one mux/server-level automated test in `internal/notes/server_test.go` that recreates the server against the same `presentations` directory and verifies `/p/{name}/` is reachable before cleanup and returns `404` after startup/prune removes the expired directory.
