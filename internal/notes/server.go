package notes

import (
	"fmt"
	"log"
	"mime"
	"net/http"
	"os"
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
	PresentationDir   string
	PresentationIndex string
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
// Defense-in-depth: reject Socket.IO handshakes that don't carry a
	// valid access token. wrapSocket() already enforces this at the
	// HTTP polling layer, but Socket.IO's WebSocket upgrade bypasses
	// mux routing entirely — without this middleware the upgrade would
	// succeed with an empty auth payload.
	if cfg.AccessToken != "" {
		sio.Use(func(so *socket.Socket, next func(*socket.ExtendedError)) {
			hs := so.Handshake()
			var authPayload map[string]any
			if hs != nil {
				authPayload = map[string]any{
					"auth": hs.Auth,
				}
			}
			if err := authorizeHandshake(cfg.AccessToken, authPayload, hs.Query); err != nil {
				next(socket.NewExtendedError("unauthorized", nil))
				return
			}
			next(nil)
		})
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
	auth := newBrowserAuth(cfg.AccessToken)

	// API endpoints for presentation upload/management (auth-protected)
	mux.HandleFunc("POST /api/presentations/{name}", requireAccessToken(cfg.AccessToken, HandleUploadPresentation(presStore)))
	mux.HandleFunc("PUT /api/presentations/{name}", requireAccessToken(cfg.AccessToken, HandleUploadPresentation(presStore)))
	mux.HandleFunc("GET /api/presentations", requireAccessToken(cfg.AccessToken, HandleListPresentations(presStore)))
	mux.HandleFunc("DELETE /api/presentations/{name}", requireAccessToken(cfg.AccessToken, HandleDeletePresentation(presStore)))

// Browser auth entry points.
	mux.HandleFunc("GET /login", auth.loginHandler())
	mux.HandleFunc("POST /login", auth.loginHandler())
	mux.HandleFunc("GET /logout", auth.logoutHandler())
	mux.HandleFunc("POST /logout", auth.logoutHandler())

	// Cross-origin cookie handoff: the publisher's presentation page POSTs
	// the token here once and we promote it to a signed session cookie on
	// our own origin. Subsequent navigations / Socket.IO connections from
	// the same browser send the cookie automatically. Mounted outside
	// auth.wrapPage so OPTIONS preflight bypasses the /login redirect —
	// the browser rejects any 3xx on a preflight.
	mux.HandleFunc("/auth-token", auth.tokenExchangeHandler())

	// Serve uploaded presentations at /p/{name}/...
	mux.HandleFunc("/p/{name}/", auth.wrapPage(HandleServePresentation(cfg.PresentationsDir)))

	// Redirect /p/{name} to /p/{name}/
	mux.HandleFunc("GET /p/{name}", auth.wrapPage(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		http.Redirect(w, r, "/p/"+name+"/", http.StatusMovedPermanently)
	})))

	// Socket.IO handler
	// Serve the embedded Socket.IO client library
	mux.HandleFunc("GET /socket.io/socket.io.js", auth.wrapPage(http.HandlerFunc(HandleSocketIOClient)))
	mux.Handle("/socket.io/", auth.wrapSocket(sio.ServeHandler(nil)))

	// Health check
	mux.HandleFunc("/health", HandleHealth)

	// Sessions JSON endpoint
	mux.HandleFunc("GET /notes/sessions", auth.wrapPage(HandleSessionsJSON(store, activeTtl, int64(cfg.ActiveTtlMs))))

	// Dashboard and speaker view
	dashboardHandler := auth.wrapPage(HandleDashboard(store, activeTtl))
	mux.HandleFunc("GET /notes", dashboardHandler)
	mux.HandleFunc("GET /notes/", dashboardHandler)
	mux.HandleFunc("GET /notes/{socketId}", auth.wrapPage(HandleSpeakerView(store, cfg.PresentationsDir, cfg.AccessToken)))

	// Static file serving and root handler
	presentationFS := http.FileServer(http.Dir(cfg.PresentationDir))
	rootHandler := HandleRoot(cfg.PresentationDir, cfg.PresentationIndex)
	mux.HandleFunc("/", auth.wrapPage(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			rootHandler(w, r)
			return
		}

		presentationFS.ServeHTTP(w, r)
	})))

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
