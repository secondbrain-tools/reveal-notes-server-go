package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"

	"remote-notes-server/internal/notes"
)

func main() {
	// Parse flags
	hostname := flag.String("hostname", "127.0.0.1", "Hostname to bind to")
	port := flag.Int("port", 1947, "Port to listen on")
	revealDir := flag.String("revealDir", getCWD(), "Directory containing reveal.js")
	presentationDir := flag.String("presentationDir", ".", "Directory containing the presentation")
	presentationIndex := flag.String("presentationIndex", "/index.html", "Presentation index file")
	pluginDir := flag.String("pluginDir", "./node_modules/remote-notes-server", "Plugin directory")
	activeTtlMs := flag.Int("activeTtlMs", 7200000, "Session TTL in milliseconds")
	presentationsDir := flag.String("presentationsDir", "presentations", "Directory for uploaded presentations")
	presentationTtlMs := flag.Int("presentationTtlMs", 86400000, "TTL for uploaded presentations in milliseconds (default: 24h)")
	accessToken := flag.String("accessToken", "", "Access token for API endpoints (empty = no auth)")
	idleShutdownMs := flag.Int("idleShutdownMs", 0, "Shut down after all clients disconnect for this many milliseconds (0 = disabled)")

	flag.Parse()

	cfg := notes.ServerConfig{
		Hostname:          *hostname,
		Port:              *port,
		RevealDir:         *revealDir,
		PresentationDir:   *presentationDir,
		PresentationIndex: *presentationIndex,
		PluginDir:         *pluginDir,
		ActiveTtlMs:       *activeTtlMs,
		PresentationsDir:  *presentationsDir,
		PresentationTtlMs: *presentationTtlMs,
		AccessToken:       *accessToken,
		IdleShutdownMs:    *idleShutdownMs,
	}

	server := notes.NewServer(cfg)

	// Print startup banner (matching Node.js version colors)
	brown := "\033[33m"
	green := "\033[32m"
	reset := "\033[0m"
	slidesLocation := fmt.Sprintf("http://%s:%s", cfg.Hostname, strconv.Itoa(cfg.Port))

	fmt.Printf("%sreveal.js - Speaker Notes%s\n", brown, reset)
	fmt.Printf("1. Open the slides at %s%s%s\n", green, slidesLocation, reset)
	fmt.Printf("   Or alternatively with QR code: %s%s?qr=true%s\n", green, slidesLocation, reset)
	fmt.Println("2. Click on the link in your JS console to go to the notes page")
	fmt.Printf("3. Active sessions JSON: %s%s/notes/sessions%s\n", green, slidesLocation, reset)

	if err := server.ListenAndServe(); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}

func getCWD() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}
