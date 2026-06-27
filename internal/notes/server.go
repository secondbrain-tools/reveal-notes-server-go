package notes

import (
	"fmt"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	etypes "github.com/zishang520/engine.io/types"
	"github.com/zishang520/socket.io/socket"
)

// ServerConfig holds configuration for the notes server.
type ServerConfig struct {
	Hostname          string
	Port              int
	RevealDir         string
	PresentationDir   string
	PresentationIndex string
	PluginDir         string
	ActiveTtlMs       int
	PresentationsDir  string // directory for uploaded presentations
	PresentationTtlMs int    // TTL for uploaded presentations in milliseconds
	AccessToken       string // if set, required for API endpoints (Bearer token)
	IdleShutdownMs    int    // if > 0, shut down after all clients disconnect for this duration
}

// Server wraps the HTTP server and Socket.IO server.
type Server struct {
	Config           ServerConfig
	Store            *SessionStore
	SocketIO         *socket.Server
	Mux              *http.ServeMux
	ActiveTtl        time.Duration
	PresStore        *PresentationStore
	PresentationTtl  time.Duration
	AccessToken      string
	IdleShutdown     time.Duration
	connectedClients atomic.Int64
	idleTimer        *time.Timer
	idleTimerMu      sync.Mutex
}

// NewServer creates and configures a new notes server.
func NewServer(cfg ServerConfig) *Server {
	mime.AddExtensionType(".js", "application/javascript")
	activeTtl := time.Duration(cfg.ActiveTtlMs) * time.Millisecond
	presentationTtl := time.Duration(cfg.PresentationTtlMs) * time.Millisecond
	if presentationTtl <= 0 {
		presentationTtl = 24 * time.Hour
	}
	if cfg.PresentationsDir == "" {
		cfg.PresentationsDir = "presentations"
	}

	idleShutdown := time.Duration(cfg.IdleShutdownMs) * time.Millisecond

	store := NewSessionStore()
	presStore := NewPresentationStore(cfg.PresentationsDir, presentationTtl)

	opts := socket.DefaultServerOptions()
	opts.SetAllowEIO3(true)
	opts.SetCors(&etypes.Cors{
		Origin:      true,
		Credentials: true,
	})
	sio := socket.NewServer(nil, opts)

	srv := &Server{
		Config:          cfg,
		Store:           store,
		SocketIO:        sio,
		Mux:             nil, // set below
		ActiveTtl:       activeTtl,
		PresStore:       presStore,
		PresentationTtl: presentationTtl,
		AccessToken:     cfg.AccessToken,
		IdleShutdown:    idleShutdown,
	}

	sio.On("connection", func(clients ...any) {
		so := clients[0].(*socket.Socket)
		srv.connectedClients.Add(1)
		srv.cancelIdleTimer()

		so.On("disconnect", func(args ...any) {
			if srv.connectedClients.Add(-1) == 0 {
				srv.startIdleTimer()
			}
		})

		so.On("new-subscriber", func(args ...any) {
			if len(args) > 0 {
				if data, ok := args[0].(map[string]any); ok {
					sockId, _ := data["socketId"].(string)
					store.Touch(sockId, data)
				}
			}
			so.Broadcast().Emit("new-subscriber", args...)
		})

		so.On("statechanged", func(args ...any) {
			if len(args) > 0 {
				if data, ok := args[0].(map[string]any); ok {
					if state, ok := data["state"].(map[string]any); ok {
						delete(state, "overview")
					}
					sockId, _ := data["socketId"].(string)
					store.Touch(sockId, data)
				}
			}
			so.Broadcast().Emit("statechanged", args...)
		})

		so.On("statechanged-speaker", func(args ...any) {
			if len(args) > 0 {
				if data, ok := args[0].(map[string]any); ok {
					if state, ok := data["state"].(map[string]any); ok {
						delete(state, "overview")
					}
				}
			}
			so.Broadcast().Emit("statechanged-speaker", args...)
		})
	})

	// Set up HTTP routes
	mux := http.NewServeMux()

	// API endpoints for presentation upload/management (auth-protected)
	mux.HandleFunc("POST /api/presentations/{name}", requireAccessToken(cfg.AccessToken, HandleUploadPresentation(presStore)))
	mux.HandleFunc("PUT /api/presentations/{name}", requireAccessToken(cfg.AccessToken, HandleUploadPresentation(presStore)))
	mux.HandleFunc("GET /api/presentations", requireAccessToken(cfg.AccessToken, HandleListPresentations(presStore)))
	mux.HandleFunc("DELETE /api/presentations/{name}", requireAccessToken(cfg.AccessToken, HandleDeletePresentation(presStore)))

	// Serve uploaded presentations at /p/{name}/...
	mux.HandleFunc("/p/{name}/", HandleServePresentation(cfg.PresentationsDir))

	// Redirect /p/{name} to /p/{name}/
	mux.HandleFunc("GET /p/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		http.Redirect(w, r, "/p/"+name+"/", http.StatusMovedPermanently)
	})

	// Socket.IO handler

	// Serve the embedded Socket.IO client library
	mux.HandleFunc("GET /socket.io/socket.io.js", HandleSocketIOClient)
	mux.Handle("/socket.io/", sio.ServeHandler(nil))

	// Health check
	mux.HandleFunc("/health", HandleHealth)

	// Sessions JSON endpoint
	mux.HandleFunc("/notes/sessions", HandleSessionsJSON(store, activeTtl, int64(cfg.ActiveTtlMs)))

	// Dashboard and speaker view (/notes handles both /notes and /notes/:socketId)
	mux.HandleFunc("/notes/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/notes" || r.URL.Path == "/notes/" {
			HandleDashboard(store, activeTtl)(w, r)
			return
		}
		if r.URL.Path == "/notes/sessions" {
			HandleSessionsJSON(store, activeTtl, int64(cfg.ActiveTtlMs))(w, r)
			return
		}
		HandleSpeakerView(store, cfg.PresentationsDir)(w, r)
	})

	// Static file serving and root handler
	revealFS := http.FileServer(http.Dir(cfg.RevealDir))
	presentationFS := http.FileServer(http.Dir(cfg.PresentationDir))
	rootHandler := HandleRoot(cfg.PresentationDir, cfg.PresentationIndex)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			rootHandler(w, r)
			return
		}
		// Try revealDir first, then presentationDir
		revealPath := filepath.Join(cfg.RevealDir, r.URL.Path)
		presPath := filepath.Join(cfg.PresentationDir, r.URL.Path)
		if _, err := os.Stat(revealPath); err == nil {
			revealFS.ServeHTTP(w, r)
		} else if _, err := os.Stat(presPath); err == nil {
			presentationFS.ServeHTTP(w, r)
		} else {
			http.NotFound(w, r)
		}
	})

	srv.Mux = mux

	// Start background pruning goroutine
	if activeTtl > 0 {
		pruneInterval := activeTtl
		if pruneInterval > 60*time.Second {
			pruneInterval = 60 * time.Second
		}
		go func() {
			ticker := time.NewTicker(pruneInterval)
			defer ticker.Stop()
			for range ticker.C {
				store.Prune(activeTtl)
			}
		}()
	}

	// Start background pruning for presentations
	go func() {
		presPruneInterval := presentationTtl / 2
		if presPruneInterval < 60*time.Second {
			presPruneInterval = 60 * time.Second
		}
		if presPruneInterval > 1*time.Hour {
			presPruneInterval = 1 * time.Hour
		}
		ticker := time.NewTicker(presPruneInterval)
		defer ticker.Stop()
		for range ticker.C {
			presStore.Prune()
		}
	}()

	return srv
}

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe() error {
	addr := fmt.Sprintf("%s:%d", s.Config.Hostname, s.Config.Port)
	return http.ListenAndServe(addr, s.Mux)
}

// cancelIdleTimer stops a pending idle shutdown timer.
func (s *Server) cancelIdleTimer() {
	s.idleTimerMu.Lock()
	defer s.idleTimerMu.Unlock()
	if s.idleTimer != nil {
		s.idleTimer.Stop()
		s.idleTimer = nil
	}
}

// startIdleTimer begins the idle shutdown countdown. When the timer fires,
// the server process exits. Only active when IdleShutdown > 0.
func (s *Server) startIdleTimer() {
	if s.IdleShutdown <= 0 {
		return
	}
	s.idleTimerMu.Lock()
	defer s.idleTimerMu.Unlock()
	if s.idleTimer != nil {
		s.idleTimer.Stop()
	}
	s.idleTimer = time.AfterFunc(s.IdleShutdown, func() {
		log.Printf("[notes-server] No clients connected for %v, shutting down.", s.IdleShutdown)
		os.Exit(0)
	})
}
