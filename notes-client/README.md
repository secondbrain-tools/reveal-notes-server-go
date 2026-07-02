# remote-notes-client

Browser-side companion for the `remote-notes-server` project.

It contains two things:

- `remote-notes-client.js` — the lightweight client that connects a Reveal-compatible presentation to the notes server
- `demo.html` — a self-contained English demo deck that explains the project and connects to `http://127.0.0.1:1947`

## Files

- `demo.html` — local demo presentation with a tiny Reveal-compatible slide runtime
- `remote-notes-client.js` — reusable browser integration
- `presentation-libs/socket.io.js` — Socket.IO browser client used by the runtime and the demo
   provided by https://github.com/socketio/ under the MIT License
## Run the demo

Start the server from the repository root:

```bash
make
./notes-server --hostname 127.0.0.1 --port 1947
```

Then open:

- `notes-client/demo.html`

The demo will:

- render the project presentation
- connect to `http://127.0.0.1:1947`
- show a live speaker-notes link once the Socket.IO connection is established

## Integration modes

### 1. Plain HTML / demo mode

```html
<script>
  window.REMOTE_NOTES_CLIENT_CONFIG = {
    serverUrl: "http://127.0.0.1:1947",
    token: "secret-token",
    socketId: "demo",
    socketIoPath: "./presentation-libs/socket.io.js",
    reveal: window.Reveal,
    revealConfig: {
      plugins: [RevealHighlight, RevealNotes],
    },
  };
</script>
<script src="./remote-notes-client.js"></script>
<script>
  window.RemoteNotesClient.init();
</script>
```

### Access token

If the server is started with `--access-token`, set the same value in one of these places:

- `window.REMOTE_NOTES_CLIENT_CONFIG.token` in plain HTML/demo mode
- `{env.REMOTE_NOTES_ACCESS_TOKEN}` in manifest/template mode

If the token is missing while the server requires one, the browser cannot join the session.

### Stable presentation mapping

Set `socketId` to the same slug as the uploaded presentation name when you want the speaker view preview to open that uploaded presentation automatically.

Example:
- upload `demo.html` as presentation `demo`
- set `socketId: "demo"`
- the notes URL becomes `/notes/demo`
- the speaker view then loads `/p/demo/` as its preview iframe

### 2. Manifest/template mode

Use the placeholders exposed by the client script:

- `{env.NOTES_SERVER_URL}`
- `{env.REMOTE_NOTES_ACCESS_TOKEN}`

Example:

```html
<script>
  window.REMOTE_NOTES_CLIENT_CONFIG = {
    serverUrl: "{env.NOTES_SERVER_URL}",
    token: "{env.REMOTE_NOTES_ACCESS_TOKEN}",
    socketId: "demo",
    socketIoPath: "./presentation-libs/socket.io.js",
    reveal: window.Reveal,
    revealConfig: {
      plugins: [RevealHighlight, RevealNotes],
    },
  };
</script>
<script src="./remote-notes-client.js"></script>
<script>
  window.RemoteNotesClient.init();
</script>
```
