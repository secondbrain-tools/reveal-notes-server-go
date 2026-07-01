# Reveal Notes Server

A Go reimplementation of the [reveal.js speaker notes server plugin](https://github.com/xcopy/reveal-notes-server), providing real-time speaker note synchronization for reveal.js presentations via Socket.IO.

## Overview

The notes server enables a "speaker view" for reveal.js presentations. As you navigate through slides, the server tracks your position and broadcasts it to connected note-taking clients in real time. It provides:

- **Active session dashboard** at `/notes` — lists all connected sessions with slide position
- **Speaker view** at `/notes/{socketId}` — individual note-taking page per session
- **JSON API** at `/notes/sessions` — machine-readable session list
- **Health endpoint** at `/health` — liveness check
- **Static file serving** for exported presentation files

## Quick Start

```bash
make
./notes-server
```

Then open the slides at the URL shown in the startup banner, open your browser's JavaScript console, and click the link to open the speaker notes page.

### Command-line flags

| Flag | Short | Default | Description |
|---|---|---|---|
| `--hostname` | `-H` | `127.0.0.1` | Hostname to bind |
| `--port` | `-p` | `1947` | Port to listen on |
| `--presentation-dir` | `-d` | `.` | Directory containing the presentation |
| `--presentation-index` | `-i` | `/index.html` | Presentation entry point |
| `--active-ttl-ms` | `-a` | `7200000` | Session TTL in milliseconds (2h) |
| `--access-token` | `-k` | (empty) | Access token for API auth and browser read sessions — empty = no auth |
| `--presentations-dir` | `-u` | `presentations` | Directory for uploaded presentations |
| `--presentation-ttl-ms` | `-t` | `86400000` | TTL for uploaded presentations in ms (24h) |
| `--idle-shutdown-ms` | `-s` | `0` | Shut down after all clients disconnect for this many milliseconds |

Legacy camelCase server flags such as `--presentationDir` and `--accessToken` still work but now print a deprecation warning.

## Building

### Prerequisites

- [Go](https://go.dev/) 1.25 or later

### Targets

```bash
make                  # Build both binaries for current platform
make build-linux-amd64
make build-linux-arm64
make build-windows-amd64
make build-darwin-amd64    # Intel Mac
make build-darwin-arm64    # Apple Silicon (M1/M2/M3)
make cross             # Build all platforms into dist/
make build-upload         # Build the upload CLI
make run               # Build and run
make test              # Run tests
make vet               # Run static analysis
make fmt               # Format code
make tidy              # Tidy module dependencies
make clean             # Remove local binaries
make distclean         # Remove all build artifacts
make help              # Show all targets
```

### Custom builds

Override `GOOS` and `GOARCH` for any Go-supported target:

```bash
make build GOOS=linux GOARCH=arm GOARM=7   # Raspberry Pi (ARMv7)
```

Go cross-compilation is lightweight — no special toolchains required. A full `make cross` of all 5 platforms completes in a few seconds.

## Project Structure

```
remote-notes-server/
├── go.mod / go.sum          # Go module definition
├── Makefile                 # Build automation
├── cmd/
│   └── notes-server/
│       └── main.go          # Entry point — parses flags, starts server
├── manifest.yaml            # Runtime manifest for the publisher
└── internal/
    └── notes/
        ├── server.go        # Server setup, Socket.IO config, HTTP routing
        ├── sessions.go      # Thread-safe session store with TTL pruning
        ├── handlers.go      # HTTP handlers — dashboard, speaker view, JSON API
        ├── templates.go     # Embedded speaker view HTML (//go:embed)
        ├── static/
        │   └── socket.io.min.js  # Embedded Socket.IO client asset
        ├── presentations.go # Presentation upload/serve store & handlers
        ├── index.html       # Speaker view template
        ├── server_test.go
        ├── sessions_test.go
        └── presentations_test.go
```

## API

### `GET /health`

```json
{ "status": "ok" }
```

### `GET /notes/sessions`

```json
{
  "count": 2,
  "activeTtlMs": 7200000,
  "sessions": [
    {
      "socketId": "abc123...",
      "createdAt": "2025-01-01T00:00:00Z",
      "lastSeenAt": "2025-01-01T00:05:00Z",
      "lastIndex": { "h": 3, "v": 1, "f": 2 }
    }
  ]
}
```

### `GET /notes`

HTML dashboard listing all active sessions with slide positions and links to individual speaker views.

### `GET /notes/{socketId}`

Individual speaker view page for a specific session.

### `POST /api/presentations/{name}`

Upload a zip file containing a self-contained reveal.js presentation. Requires `Authorization: Bearer <token>` if `--access-token` is set. Accepts multipart form with field name `file`.
The upload CLI packages the source folder and sends this request automatically.

```bash
curl -F "file=@presentation.zip" http://localhost:1947/api/presentations/my-talk
```

Response (201):
```json
{
  "name": "my-talk",
  "createdAt": "2026-05-11T10:00:00Z",
  "size": 1048576,
  "sizeHuman": "1.0 MB"
}
```

Uploaded presentations are stored under `--presentations-dir/{name}` with persisted metadata so list/cleanup survive restarts, and are auto-deleted after `--presentation-ttl-ms`. Max upload size: 100 MB.

### `GET /api/presentations/{name}/hash`

Return the SHA-256 hash of the last uploaded zip archive for a presentation. Requires `Authorization: Bearer <token>` if `--access-token` is set. Returns `404` when the presentation does not exist or no hash is available (older uploads created before the hash feature).

```json
{
  "name": "my-talk",
  "hash": "sha256:abc123def456..."
}
```

This endpoint enables clients to compare local and remote archive hashes before uploading, avoiding unnecessary data transfer when the presentation hasn't changed.

### `GET /api/presentations`

List all uploaded presentations (sorted by creation time, newest first). The list is rebuilt from stored metadata on startup and requires auth if `--access-token` is set.

```json
{
  "count": 2,
  "presentations": [
    {
      "name": "my-talk",
      "createdAt": "2026-05-11T10:00:00Z",
      "size": 1048576,
      "sizeHuman": "1.0 MB"
    }
  ]
}
```

### `DELETE /api/presentations/{name}`

Delete an uploaded presentation. Requires auth if `--access-token` is set.

```json
{ "status": "deleted", "name": "my-talk" }
```

### `GET /p/{name}/`

Serve an uploaded presentation's files. Maps to `--presentations-dir/{name}` on disk. Expired uploads are pruned from disk using the stored metadata, so stale directories stop serving after restart/cleanup. The root serves `index.html`. Protected by browser login when `--access-token` is set; bearer auth still works for API clients.

```bash
# View a presentation
open http://localhost:1947/p/my-talk/
```

## Authentication

If `--access-token` is set, browser read routes (`/`, `/p/{name}/`, `/notes`, `/notes/{socketId}`, `/notes/sessions`, `/socket.io/`) require a login once per browser session. The server issues an `HttpOnly` cookie (`SameSite=Lax`, `Secure` on HTTPS) after a successful token submission at `/login`. Any protected route can also be opened with a one-time `?token=<access-token>` query string (magic link) — the request is authenticated and the browser cookie is **not** set automatically, so the user can choose between a session cookie and a stateless magic link.

Socket.IO connections accept the access token in any of three places, and the server checks all of them so the right path works for every transport and entry point:

1. **`Authorization: Bearer <token>`** header — works for HTTP long-polling; ideal for server-to-server clients.
2. **`?token=<token>`** query string on the `/socket.io/` URL — works for both polling and WebSocket transports, including cross-origin browsers that can't set custom headers on the WebSocket upgrade.
3. **`auth: { token }`** payload in the Socket.IO handshake (`io(url, { auth: { token } })`) — validated by a server-side `socket.Use(...)` middleware that rejects unauthorized handshakes with a clean `connect_error`. This is the recommended path for embedded clients that already have the token in the page (e.g. the `remote-notes-client` runtime).

API write/list/delete endpoints keep the existing bearer-token flow for machines and scripts:

```bash
# Start the server with a token
./notes-server --access-token=my-secret-token

# Browser login
open http://localhost:1947/login

# Authenticated API request
curl -H "Authorization: Bearer my-secret-token" \
  -F "file=@my-talk.zip" \
  http://localhost:1947/api/presentations/my-talk

# Unauthenticated API request → 401
curl -F "file=@my-talk.zip" http://localhost:1947/api/presentations/my-talk
# → {"error":"unauthorized"}
```

`/health` remains open, and when `--access-token` is empty the server behaves exactly as before.

## Upload CLI

A local helper binary is available for packaging and uploading exported presentations.
It walks a source folder, renames the chosen HTML entry file to `index.html`, preserves the other relative paths, and applies repeatable gitignore-style `--ignore` patterns.

```bash
make build-upload
./upload-presentation \
  --server-url=http://host:1947 \
  --name=my-talk \
  --source-dir=./output \
  --html-file=index.html \
  --access-token=my-secret-token \
  --ignore='*.map' \
  --ignore='node_modules/'
```

Uploader flags:

| Flag | Short | Description |
|---|---|---|
|| `--server-url` | `-u` | Server base URL *(required)* |
|| `--name` | `-n` | Presentation slug/name *(inferred; see below)* |
|| `--source-dir` | `-s` | Presentation folder to package *(inferred; see below)* |
|| `--html-file` | `-f` | HTML file inside the source folder to publish as `index.html` *(required)* |
| `--access-token` | `-k` | Optional bearer token |
| `--ignore` | `-i` | Repeatable ignore pattern |

Legacy uploader flags `--source` and `--html` still work but now print a deprecation warning.

The uploader is meant for local/developer use only and is not part of the runtime manifest.

### Inference from `--html-file`

When `--html-file` is set, the following flags can be omitted and will be derived from its path. Explicit values always win over inference.

| Flag | Inferred value |
| --- | --- |
| `--name` | Basename of `--html-file` with its extension stripped (e.g. `--html-file=…/out/my-talk.html` → `--name=my-talk`) |
| `--source-dir` | Directory containing `--html-file` (e.g. `--html-file=…/out/my-talk.html` → `--source-dir=…/out`) |
| `--filelist` | `<name>.filelist.txt` sibling of `--html-file`, used **only** when that file exists. Missing siblings are reported as `(not found)` in the inference summary |

At startup the CLI prints the resolved values to **stderr** with an `(inferred)` or `(provided)` tag so you can verify the inference before the upload runs:

```bash
$ ./upload-presentation \
    --server-url=http://127.0.0.1:1947 \
    --html-file=~/Code/Logseq-Publisher/output/HS-Heilbronn/HS-Heilbronn-Softwareentwicklung_mit_KI_-_Best_Practice_und_Ausblick.html
Inferred from --html-file:
  --html-file   = /home/st6ka8/Code/Logseq-Publisher/output/HS-Heilbronn/HS-Heilbronn-Softwareentwicklung_mit_KI_-_Best_Practice_und_Ausblick.html  (provided)
  --name        = HS-Heilbronn-Softwareentwicklung_mit_KI_-_Best_Practice_und_Ausblick  (inferred)
  --source-dir  = /home/st6ka8/Code/Logseq-Publisher/output/HS-Heilbronn  (inferred)
  --filelist    = /home/st6ka8/Code/Logseq-Publisher/output/HS-Heilbronn/HS-Heilbronn-Softwareentwicklung_mit_KI_-_Best_Practice_und_Ausblick.filelist.txt  (inferred)
```

If a sibling filelist is missing, only `--name` and `--source-dir` are inferred and the upload continues without a filelist:

```bash
$ ./upload-presentation --server-url=http://host:1947 --html-file=…/out/no-flist-talk.html
Inferred from --html-file:
  --html-file   = …/out/no-flist-talk.html  (provided)
  --name        = no-flist-talk  (inferred)
  --source-dir  = …/out  (inferred)
  --filelist    = (not found; sibling …/out/no-flist-talk.filelist.txt does not exist)
```

### Skip-if-unchanged

Before uploading, the CLI computes a SHA-256 hash of the local archive and queries `GET /api/presentations/{name}/hash`. If the remote hash matches the local hash, the upload is skipped:

```bash
./upload-presentation --server-url=http://host:1947 --name=my-talk --source-dir=./output --html-file=index.html
# Presentation "my-talk" is already up-to-date (hash sha256:abc123...). Skipping upload.
```

This avoids unnecessary data transfer when the presentation content hasn't changed since the last upload.

## Presentation Upload & Serving

End-to-end workflow for serving a presentation on a remote server:

```bash
# 1. Zip the output directory from your local machine
cd /path/to/Logseq-Publisher
zip -r /tmp/my-talk.zip output/

# 2. Upload to the server (with auth if configured)
curl -H "Authorization: Bearer my-secret-token" \
  -F "file=@/tmp/my-talk.zip" \
  http://server.example.com:1947/api/presentations/my-talk

# 3. Open it
open http://server.example.com:1947/p/my-talk/

# 4. Clean up when done
curl -X DELETE \
  -H "Authorization: Bearer my-secret-token" \
  http://server.example.com:1947/api/presentations/my-talk
```

---
## How It Works

1. A reveal.js presentation connects to the Socket.IO server (`/socket.io/`)
2. When the presenter navigates slides, reveal.js emits `statechanged` events with the current `{indexh, indexv, indexf}` position
3. The Go server broadcasts these events to all other connected clients
4. A speaker notes page opened at `/notes/{socketId}` receives these events and updates its note display in real time
5. Sessions are automatically pruned after `activeTtlMs` of inactivity (default: 2 hours)

## License

MIT — see the [original reveal-notes-server](https://github.com/xcopy/reveal-notes-server) for details.
