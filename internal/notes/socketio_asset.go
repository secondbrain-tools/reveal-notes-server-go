package notes

import (
	_ "embed"
	"net/http"
)

//go:embed static/socket.io.min.js
var socketIOClientJS []byte

// HandleSocketIOClient serves the embedded Socket.IO client library.
func HandleSocketIOClient(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(socketIOClientJS)
}
