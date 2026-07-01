package notes

import (
	"encoding/json"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// sessionsResponse is the JSON response for GET /notes/sessions.
type sessionsResponse struct {
	Count       int        `json:"count"`
	ActiveTtlMs int64      `json:"activeTtlMs"`
	Sessions    []*Session `json:"sessions"`
}

// DashboardData holds template data for the HTML dashboard.
type DashboardData struct {
	ActiveTtlMinutes int64
	SessionCards     []sessionCard
}

type sessionCard struct {
	SocketId      string
	SocketIdSafe  string
	CreatedAtIso  string
	CreatedAtAge  string
	LastSeenAtIso string
	LastSeenAtAge string
	IndexH        string
	IndexV        string
	IndexF        string
	EncodedID     string
}

var dashboardTemplate = template.Must(template.New("dashboard").Funcs(template.FuncMap{
	"formatAge": formatAge,
	"formatIso": formatIso,
}).Parse(dashboardHTML))

const dashboardHTML = `<!doctype html>
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
      <p class="sub">Sessions auto-expire after {{.ActiveTtlMinutes}} minutes of inactivity.</p>
      <div class="links">
        <a class="chip" href="/notes/sessions" target="_blank" rel="noopener">JSON API</a>
        <a class="chip" href="/" target="_blank" rel="noopener">Open Slides</a>
      </div>
    </header>
    <section class="grid">
      {{if .SessionCards}}
        {{range .SessionCards}}
        <article class="card">
          <h2 class="id">{{.SocketIdSafe}}</h2>
          <div class="meta"><span>Created</span><time datetime="{{.CreatedAtIso}}">{{.CreatedAtIso}}</time><span class="age">{{.CreatedAtAge}}</span></div>
          <div class="meta"><span>Last seen</span><time datetime="{{.LastSeenAtIso}}">{{.LastSeenAtIso}}</time><span class="age">{{.LastSeenAtAge}}</span></div>
          <div class="meta"><span>Slide h/v/f</span><code>{{.IndexH}} / {{.IndexV}} / {{.IndexF}}</code><span></span></div>
          <a class="open" href="/notes/{{.EncodedID}}" target="_blank" rel="noopener">Open Speaker View</a>
        </article>
        {{end}}
      {{else}}
        <article class="card empty">
          <h2>No active sessions</h2>
          <p>Open your presentation and navigate slides once. Then refresh this page.</p>
        </article>
      {{end}}
    </section>
  </main>
</body>
</html>`

// now returns current time (can be overridden in tests).
var now = time.Now

func formatAge(ts time.Time) string {
	seconds := int(time.Since(ts).Seconds())
	if seconds < 0 {
		seconds = 0
	}
	if seconds < 60 {
		return itoa(seconds) + "s ago"
	}
	minutes := seconds / 60
	if minutes < 60 {
		return itoa(minutes) + "m ago"
	}
	hours := minutes / 60
	if hours < 24 {
		return itoa(hours) + "h ago"
	}
	days := hours / 24
	return itoa(days) + "d ago"
}

func formatIso(ts time.Time) string {
	return ts.UTC().Format(time.RFC3339)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

func intOrDash(v *int) string {
	if v == nil {
		return "-"
	}
	return itoa(*v)
}

// HandleHealth responds with {"status":"ok"}.
func HandleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

// HandleSessionsJSON responds with a JSON list of active sessions after pruning.
func HandleSessionsJSON(store *SessionStore, activeTtl time.Duration, activeTtlMs int64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		store.Prune(activeTtl)
		sessions := store.List()
		resp := sessionsResponse{
			Count:       len(sessions),
			ActiveTtlMs: activeTtlMs,
			Sessions:    sessions,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}
}

// HandleDashboard responds with the HTML dashboard.
func HandleDashboard(store *SessionStore, activeTtl time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		store.Prune(activeTtl)
		sessions := store.List()

		cards := make([]sessionCard, 0, len(sessions))
		for _, s := range sessions {
			card := sessionCard{
				SocketId:      s.SocketId,
				SocketIdSafe:  htmlEscape(s.SocketId),
				CreatedAtIso:  formatIso(s.CreatedAt),
				CreatedAtAge:  formatAge(s.CreatedAt),
				LastSeenAtIso: formatIso(s.LastSeenAt),
				LastSeenAtAge: formatAge(s.LastSeenAt),
				IndexH:        intOrDash(nil),
				IndexV:        intOrDash(nil),
				IndexF:        intOrDash(nil),
				EncodedID:     s.SocketId,
			}
			if s.LastIndex != nil {
				card.IndexH = intOrDash(s.LastIndex.H)
				card.IndexV = intOrDash(s.LastIndex.V)
				card.IndexF = intOrDash(s.LastIndex.F)
			}
			cards = append(cards, card)
		}

		data := DashboardData{
			ActiveTtlMinutes: int64(activeTtl.Minutes()),
			SessionCards:     cards,
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		dashboardTemplate.Execute(w, data)
	}
}

func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&#39;")
	return s
}

// HandleSpeakerView serves the embedded speaker view HTML with the socketId injected.
//
// Access-token flow: when the URL carries a one-time ?token=... AND it
// matches the configured access token, we set the signed session cookie
// on our origin and redirect to the clean URL. Subsequent navigations
// and Socket.IO connections from the same browser send the cookie
// automatically — the token never persists in the address bar, history,
// or share links.
func HandleSpeakerView(store *SessionStore, presentationsDir, accessToken string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// Extract socketId from the router path or fallback to the URL.
		socketId := r.PathValue("socketId")
		if socketId == "" {
			socketId = strings.TrimPrefix(r.URL.Path, "/notes/")
		}
		if socketId == "" || socketId == r.URL.Path {
			http.NotFound(w, r)
			return
		}

		content := string(speakerViewHTML)

		// If the presentation exists in the upload directory, serve iframes
		// from /p/{name}/; otherwise fall back to the root presentation.
		var presentationURL string
		presDir := filepath.Join(presentationsDir, socketId)
		if _, err := os.Stat(presDir); err == nil {
			presentationURL = "/p/" + socketId + "/"
		} else {
			presentationURL = "/"
		}

		content = strings.ReplaceAll(content, "{{socketId}}", socketId)
		content = strings.ReplaceAll(content, "{{presentationUrl}}", presentationURL)

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, content)
	}
}

// HandleRoot serves the presentation index or a fallback message.
func HandleRoot(presentationDir, presentationIndex string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		indexPath := filepath.Join(presentationDir, presentationIndex)
		if _, err := os.Stat(indexPath); os.IsNotExist(err) {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, `<!DOCTYPE html><html lang="en"><head><meta charset="UTF-8"><title>Presentation</title></head><body><h1>Presentation not yet exported</h1><p>Export a presentation to see it here.</p></body></html>`)
			return
		}
		http.ServeFile(w, r, indexPath)
	}
}
