# Reveal Notes Server

A Go reimplementation of the [reveal.js speaker notes server plugin](https://github.com/xcopy/reveal-notes-server), providing real-time speaker note synchronization for reveal.js presentations via Socket.IO.

## Overview

The notes server enables a "speaker view" for reveal.js presentations. As you navigate through slides, the server tracks your position and broadcasts it to connected note-taking clients in real time. It provides:

- **Active session dashboard** at `/notes` — lists all connected sessions with slide position
- **Speaker view** at `/notes/{socketId}` — individual note-taking page per session
- **JSON API** at `/notes/sessions` — machine-readable session list
- **Health endpoint** at `/health` — liveness check
- **Static file serving** for both reveal.js assets and exported presentation files

## Quick Start

```bash
make
./notes-server
```

Then open the slides at the URL shown in the startup banner, open your browser's JavaScript console, and click the link to open the speaker notes page.

### Command-line flags

| Flag | Default | Description |
|---|---|---|
| `--hostname` | `127.0.0.1` | Hostname to bind |
| `--port` | `1947` | Port to listen on |
| `--revealDir` | CWD | Directory containing reveal.js |
| `--presentationDir` | `.` | Directory containing the presentation |
| `--presentationIndex` | `/index.html` | Presentation entry point |
| `--activeTtlMs` | `7200000` | Session TTL in milliseconds (2h) |
| `--accessToken` | (empty) | Access token for API auth — empty = no auth |
| `--presentationsDir` | `presentations` | Directory for uploaded presentations |
| `--presentationTtlMs` | `86400000` | TTL for uploaded presentations in ms (24h) |

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
├── notes-server.js          # Node.js implementation (alternative server)
├── manifest.yaml            # Runtime manifest for the publisher
└── internal/
    └── notes/
        ├── server.go        # Server setup, Socket.IO config, HTTP routing
        ├── sessions.go      # Thread-safe session store with TTL pruning
        ├── handlers.go      # HTTP handlers — dashboard, speaker view, JSON API
        ├── templates.go     # Embedded speaker view HTML (//go:embed)
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

Upload a zip file containing a self-contained reveal.js presentation. Requires `Authorization: Bearer <token>` if `--accessToken` is set. Accepts multipart form with field name `file`.
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

Uploaded presentations are stored under `--presentationsDir/{name}` and auto-deleted after `--presentationTtlMs`. Max upload size: 100 MB.

### `GET /api/presentations`

List all uploaded presentations (sorted by creation time, newest first). Requires auth if `--accessToken` is set.

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

Delete an uploaded presentation. Requires auth if `--accessToken` is set.

```json
{ "status": "deleted", "name": "my-talk" }
```

### `GET /p/{name}/`

Serve an uploaded presentation's files. Maps to `--presentationsDir/{name}` on disk. The root serves `index.html`. No authentication required.

```bash
# View a presentation
open http://localhost:1947/p/my-talk/
```

## Authentication

If `--accessToken` is set, write endpoints require an `Authorization` header. Read-only endpoints (`/p/`, `/notes/`, `/health`) remain open.

```bash
# Start the server with a token
./notes-server --accessToken=my-secret-token

# Authenticated request
curl -H "Authorization: Bearer my-secret-token" \
  -F "file=@my-talk.zip" \
  http://localhost:1947/api/presentations/my-talk

# Unauthenticated → 401
curl -F "file=@my-talk.zip" http://localhost:1947/api/presentations/my-talk
# → {"error":"unauthorized"}
```

## Upload CLI

A local helper binary is available for packaging and uploading exported presentations.
It walks a source folder, renames the chosen HTML entry file to `index.html`, preserves the other relative paths, and applies repeatable gitignore-style `--ignore` patterns.

```bash
make build-upload
./upload-presentation \
  --server-url=http://host:1947 \
  --name=my-talk \
  --source=./output \
  --html=index.html \
  --access-token=my-secret-token \
  --ignore='*.map' \
  --ignore='node_modules/'
```

The uploader is meant for local/developer use only and is not part of the runtime manifest.

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
