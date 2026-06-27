#!/usr/bin/env node

const http = require("http");
const express = require("express");
const fs = require("fs");
const mustache = require("mustache");
const yargs = require("yargs/yargs");
const { hideBin } = require("yargs/helpers");

const argv = yargs(hideBin(process.argv)).argv;

const app = express();
const server = http.createServer(app);
const io = require("socket.io")(server);

const opts = {
  hostname: argv.hostname || "127.0.0.1",
  port: argv.port || 1947,
  revealDir: argv.revealDir || process.cwd(),
  presentationDir: argv.presentationDir || ".",
  presentationIndex: argv.presentationIndex || "/index.html",
  pluginDir: argv.pluginDir || "./node_modules/remote-notes-server",
  activeTtlMs: Number.parseInt(String(argv.activeTtlMs || 120 * 60 * 1000), 10),
};

const sessions = new Map();
const now = () => Date.now();
const escapeHtml = (value) =>
  String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
const formatIso = (ts) => new Date(ts).toISOString();
const formatAge = (ts) => {
  const seconds = Math.max(0, Math.floor((now() - ts) / 1000));
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  return `${days}d ago`;
};

const touchSession = (socketId, payload) => {
  if (!socketId) return;
  const current = sessions.get(socketId) || { socketId, createdAt: now() };
  const next = {
    ...current,
    lastSeenAt: now(),
  };
  if (payload?.state) {
    next.lastState = payload.state;
    next.lastIndex = {
      h: payload.state.indexh ?? null,
      v: payload.state.indexv ?? null,
      f: payload.state.indexf ?? null,
    };
  }
  sessions.set(socketId, next);
};

const pruneSessions = () => {
  const cutoff = now() - opts.activeTtlMs;
  for (const [key, value] of sessions.entries()) {
    if ((value.lastSeenAt || 0) < cutoff) sessions.delete(key);
  }
};

io.on("connection", (socket) => {
  socket.on("new-subscriber", (data) => {
    touchSession(data?.socketId, data);
    socket.broadcast.emit("new-subscriber", data);
  });

  socket.on("statechanged", (data) => {
    if (data?.state) delete data.state.overview;
    touchSession(data?.socketId, data);
    socket.broadcast.emit("statechanged", data);
  });

  socket.on("statechanged-speaker", (data) => {
    if (data?.state) delete data.state.overview;
    socket.broadcast.emit("statechanged-speaker", data);
  });
});

app.use(express.static(opts.revealDir));
app.use(express.static(opts.presentationDir));

app.get("/health", (req, res) => {
  res.status(200).json({ status: "ok" });
});

app.get("/", (req, res) => {
  const indexPath = opts.presentationDir + opts.presentationIndex;
  if (!fs.existsSync(indexPath)) {
    res.writeHead(200, { "Content-Type": "text/html" });
    res.end(`<!DOCTYPE html><html lang="en"><head><meta charset="UTF-8"><title>Presentation</title></head><body><h1>Presentation not yet exported</h1><p>Export a presentation to see it here.</p></body></html>`);
    return;
  }
  res.writeHead(200, { "Content-Type": "text/html" });
  const stream = fs.createReadStream(indexPath);
  stream.on("error", (_err) => {
    if (!res.headersSent) {
      res.writeHead(500, { "Content-Type": "text/plain" });
    }
    res.end("Error reading presentation file");
  });
  stream.pipe(res);
});

app.get("/notes/sessions", (req, res) => {
  pruneSessions();
  const list = Array.from(sessions.values())
    .map((session) => ({
      socketId: session.socketId,
      createdAt: session.createdAt,
      lastSeenAt: session.lastSeenAt || session.createdAt,
      lastIndex: session.lastIndex || { h: null, v: null, f: null },
    }))
    .sort((a, b) => b.lastSeenAt - a.lastSeenAt);
  res.json({
    count: list.length,
    activeTtlMs: opts.activeTtlMs,
    sessions: list,
  });
});

app.get("/notes", (req, res) => {
  pruneSessions();
  const list = Array.from(sessions.values())
    .map((session) => ({
      socketId: session.socketId,
      createdAt: session.createdAt,
      createdAtIso: formatIso(session.createdAt),
      createdAtAge: formatAge(session.createdAt),
      lastSeenAt: session.lastSeenAt || session.createdAt,
      lastSeenAtIso: formatIso(session.lastSeenAt || session.createdAt),
      lastSeenAtAge: formatAge(session.lastSeenAt || session.createdAt),
      lastIndex: session.lastIndex || { h: null, v: null, f: null },
    }))
    .sort((a, b) => b.lastSeenAt - a.lastSeenAt);

  const rows = list
    .map((session) => {
      const index = `${session.lastIndex.h ?? "-"} / ${session.lastIndex.v ?? "-"} / ${session.lastIndex.f ?? "-"}`;
      const id = escapeHtml(session.socketId);
      const link = `/notes/${encodeURIComponent(session.socketId)}`;
      return `
        <article class="card">
          <h2 class="id">${id}</h2>
          <div class="meta"><span>Created</span><time datetime="${session.createdAtIso}">${session.createdAtIso}</time><span class="age">${session.createdAtAge}</span></div>
          <div class="meta"><span>Last seen</span><time datetime="${session.lastSeenAtIso}">${session.lastSeenAtIso}</time><span class="age">${session.lastSeenAtAge}</span></div>
          <div class="meta"><span>Slide h/v/f</span><code>${index}</code><span></span></div>
          <a class="open" href="${link}" target="_blank" rel="noopener">Open Speaker View</a>
        </article>
      `;
    })
    .join("");

  const empty = `
    <article class="card empty">
      <h2>No active sessions</h2>
      <p>Open your presentation and navigate slides once. Then refresh this page.</p>
    </article>
  `;

  res.send(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>Active Notes Sessions</title>
  <style>
    :root {
      --bg: #f6f7fb;
      --surface: #ffffff;
      --text: #112131;
      --muted: #586779;
      --line: #d6dde6;
      --accent: #0050b8;
      --accent-2: #0a74da;
      --shadow: 0 10px 20px rgba(16, 40, 70, 0.08);
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      background: linear-gradient(180deg, #edf2f8 0%, var(--bg) 30%, var(--bg) 100%);
      color: var(--text);
      font-family: "Segoe UI", system-ui, -apple-system, sans-serif;
    }
    main {
      width: min(980px, 100%);
      margin: 0 auto;
      padding: 1rem;
    }
    .top {
      background: var(--surface);
      border: 1px solid var(--line);
      border-radius: 14px;
      padding: 1rem;
      box-shadow: var(--shadow);
      margin-bottom: 0.9rem;
    }
    h1 { margin: 0 0 0.3rem; font-size: clamp(1.2rem, 4vw, 1.8rem); }
    .sub { margin: 0; color: var(--muted); font-size: 0.95rem; }
    .links { margin-top: 0.7rem; display: flex; gap: 0.6rem; flex-wrap: wrap; }
    .chip {
      border: 1px solid var(--line);
      border-radius: 999px;
      padding: 0.35rem 0.7rem;
      color: var(--accent);
      text-decoration: none;
      background: #f7fbff;
      font-size: 0.9rem;
    }
    .grid {
      display: grid;
      gap: 0.75rem;
      grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
    }
    .card {
      background: var(--surface);
      border: 1px solid var(--line);
      border-radius: 14px;
      padding: 0.9rem;
      box-shadow: var(--shadow);
      display: grid;
      gap: 0.55rem;
    }
    .id {
      margin: 0;
      font-size: 1rem;
      overflow-wrap: anywhere;
    }
    .meta {
      display: grid;
      grid-template-columns: 76px 1fr auto;
      gap: 0.4rem;
      align-items: center;
      font-size: 0.84rem;
      color: var(--muted);
    }
    .meta code {
      color: var(--text);
      background: #f1f5f9;
      border: 1px solid #e2e8f0;
      border-radius: 7px;
      padding: 0.2rem 0.35rem;
      font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
      font-size: 0.8rem;
      width: fit-content;
    }
    .age { color: #2f455d; font-weight: 600; }
    .open {
      margin-top: 0.2rem;
      text-align: center;
      text-decoration: none;
      color: white;
      background: linear-gradient(120deg, var(--accent) 0%, var(--accent-2) 100%);
      border-radius: 10px;
      padding: 0.65rem 0.8rem;
      font-weight: 600;
    }
    .empty p {
      margin: 0;
      color: var(--muted);
      font-size: 0.95rem;
    }
    @media (max-width: 520px) {
      main { padding: 0.75rem; }
      .meta { grid-template-columns: 1fr; }
      .meta span:first-child { font-weight: 600; color: #2f455d; }
    }
  </style>
</head>
<body>
  <main>
    <header class="top">
      <h1>Active Notes Sessions</h1>
      <p class="sub">Sessions auto-expire after ${Math.floor(opts.activeTtlMs / 60000)} minutes of inactivity.</p>
      <div class="links">
        <a class="chip" href="/notes/sessions" target="_blank" rel="noopener">JSON API</a>
        <a class="chip" href="/" target="_blank" rel="noopener">Open Slides</a>
      </div>
    </header>
    <section class="grid">
      ${rows || empty}
    </section>
  </main>
</body>
</html>`);
});

app.get("/notes/:socketId", (req, res) => {
  fs.readFile(opts.pluginDir + "/index.html", (err, data) => {
    if (err) {
      res.status(500).send("Unable to render notes view.");
      return;
    }
    res.send(
      mustache.render(data.toString(), {
        socketId: req.params.socketId,
      }),
    );
  });
});

setInterval(pruneSessions, Math.min(opts.activeTtlMs, 60 * 1000));

server.listen(opts.port, opts.hostname, () => {
  const brown = "\u001b[33m";
  const green = "\u001b[32m";
  const reset = "\u001b[0m";
  const slidesLocation = `http://${opts.hostname}:${opts.port}`;

  console.log(`${brown}reveal.js - Speaker Notes${reset}`);
  console.log(`1. Open the slides at ${green}${slidesLocation}${reset}`);
  console.log(
    `Or alternatively with QR code: ${green}${slidesLocation}?qr=true${reset}`,
  );
  console.log("2. Click on the link in your JS console to go to the notes page");
  console.log(
    `3. Active sessions JSON: ${green}${slidesLocation}/notes/sessions${reset}`,
  );
});
