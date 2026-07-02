# Reveal Notes Server Go 

A Go-based extension of the [reveal.js speaker notes server plugin](https://github.com/reveal/notes-server).

Compared to the original project, this repo adds:

- presentation upload from a local HTML file + directory or files from an upload list
- automatic removal of uploaded presentations after a TTL
- optional access-token based security

The current main usecase is, to use a second device for showing the notes of a presentation.

## Overview

### `notes-server`
Runs the speaker-notes server and serves:
- `/` or `/notes` — session overview
- `/notes/{socketId}` — live speaker view for a session
- `/api/presentations/*` — upload/list/delete/hash API for presentations
- `/p/{name}/` — uploaded presentation files
- `/health` — liveness check

By default it stores uploaded presentations in `./presentations` and creates that directory on startup if needed.

### `upload-presentation`
Packages a local presentation folder into a zip archive, renames the selected HTML file to `index.html`, applies ignore rules, and uploads it to the server.

It can infer missing parameters from `--html-file`.

## Reveal.js integration

If your presentation already uses Reveal.js, you can attach the remote notes client to the existing deck instead of using the local demo.

```html
<script>
window.REMOTE_NOTES_CLIENT_CONFIG = {
  serverUrl: "http://localhost:1947",
  socketId: "my-talk",
  socketIoPath: "./presentation-libs/socket.io.js",
  reveal: window.Reveal,
  revealConfig: {
    plugins: [RevealHighlight, RevealNotes],
  },
};
</script>
<script src="./remote-notes-client.js"></script>
<script>window.RemoteNotesClient.init();</script>
```

A few tips:

- `reveal` can point to your existing `window.Reveal` instance.
- `revealConfig` is passed to `Reveal.initialize()`; include `RevealNotes` and any other plugins your deck needs.
- Set `socketId` to the uploaded presentation name if you want `/notes/{socketId}` to open the matching uploaded slide deck automatically.
- If the server runs with `--access-token`, also set `token` in the config.

See `notes-client/README.md` and `notes-client/demo.html` for a full browser-side example.

## Getting started

```bash
# 1. Start the server
./notes-server --port=1947

# 2. Upload a presentation
./upload-presentation --server-url=http://localhost:1947 --html-file=notes-client/demo.html

# 3. Open the local demo page and click the link there
#    Or open http://localhost:1947 for the session overview
```

## Command-line flags

### `notes-server`

| Flag | Short | Default | Description |
|---|---|---|---|
| `--hostname` | `-H` | `127.0.0.1` | Hostname to bind to |
| `--port` | `-p` | `1947` | Port to listen on |
| `--presentation-dir` | `-d` | `.` | Directory containing the local presentation served at `/` |
| `--presentation-index` | `-i` | `/index.html` | Presentation entry file inside `--presentation-dir` |
| `--active-ttl-ms` | `-a` | `7200000` | Session TTL in milliseconds (2h) |
| `--access-token` | `-k` | (empty) | Optional bearer token for browser/API auth |
| `--presentations-dir` | `-u` | `presentations` | Directory for uploaded presentations; created on startup if missing |
| `--presentation-ttl` | `-t` | `never` | TTL for uploaded presentations (`never` disables pruning; supports Go durations plus `d`, e.g. `7d`, `4h30m`) |
| `--idle-shutdown-ms` | `-s` | `0` | Shut down after all clients disconnect for this many milliseconds |


### `upload-presentation`

| Flag | Short | Default | Description |
|---|---|---|---|
| `--server-url` | `-u` | required | Notes server base URL |
| `--html-file` | `-f` | required | Local HTML file to package and upload |
| `--name` | `-n` | inferred | Presentation slug/name |
| `--source-dir` | `-s` | inferred | Folder to package into the archive |
| `--filelist` | `-l` | inferred | Optional filelist file to include only selected paths |
| `--access-token` | `-k` | (empty) | Optional bearer token for protected servers; also enables built-in auth throttling and protected `/health` |
| `--ignore` | `-i` | empty | Repeatable gitignore-style ignore pattern |

### Upload inference

If you pass only `--server-url` and `--html-file`, the uploader can usually infer the rest:

- `--name` from the HTML filename
- `--source-dir` from the HTML file's directory
- `--filelist` from a sibling `<name>.filelist.txt` when present

The CLI prints the resolved values at startup so you can verify the inference before upload.

## Upload flow

1. `upload-presentation` builds a zip archive from `--source-dir`
2. The chosen `--html-file` becomes `index.html` inside the archive
3. Ignore patterns and optional filelists filter the packaged files
4. The archive is uploaded to `POST /api/presentations/{name}`
5. The server stores it under `--presentations-dir/{name}` and auto-removes it after `--presentation-ttl`

## Authentication

If `--access-token` is set on the server:

- browser routes require a login once per browser session
- successful `/login` redirects return the clean target URL; the access token is not appended to the redirect
- API write/list/delete/hash endpoints require `Authorization: Bearer <token>`
- `/health` is protected by the same browser/token auth flow
- the publisher-generated speaker bootstrap link still uses a one-time `?token=...` handoff for the first speaker open
- failed auth attempts are throttled with built-in fixed limits

## API quick reference

- `GET /health`
- `GET /notes`
- `GET /notes/{socketId}`
- `GET /notes/sessions`
- `POST /api/presentations/{name}`
- `GET /api/presentations`
- `DELETE /api/presentations/{name}`
- `GET /api/presentations/{name}/hash`
- `GET /p/{name}/`

## Build

```bash
make
make test
make run
```

## Nix

```bash
nix build .#notes-server
nix build .#upload-presentation
nix run .
nix develop
```

This flake also exports a NixOS module as `nixosModules.default`.
You can import it from another flake and configure
`services.remote-notes-server`.

See `docs/nix.md` for a complete example.

## License

MIT — see the original project for details.
