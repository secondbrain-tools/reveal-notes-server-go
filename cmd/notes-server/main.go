package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/pflag"

	"remote-notes-server/internal/notes"
)

func main() {
	var hostname string
	var presentationDir string
	var presentationIndex string
	var presentationsDir string
	var accessToken string
	var port int
	var activeTtlMs int
	var presentationTtlMs int
	var idleShutdownMs int

	pflag.StringVarP(&hostname, "hostname", "H", "127.0.0.1", "Hostname to bind to")
	pflag.IntVarP(&port, "port", "p", 1947, "Port to listen on")
	pflag.StringVarP(&presentationDir, "presentation-dir", "d", ".", "Directory containing the presentation")
	pflag.StringVarP(&presentationIndex, "presentation-index", "i", "/index.html", "Presentation index file")
	pflag.IntVarP(&activeTtlMs, "active-ttl-ms", "a", 7200000, "Session TTL in milliseconds")
	pflag.StringVarP(&presentationsDir, "presentations-dir", "u", "presentations", "Directory for uploaded presentations")
	pflag.IntVarP(&presentationTtlMs, "presentation-ttl-ms", "t", 86400000, "TTL for uploaded presentations in milliseconds (default: 24h)")
	pflag.StringVarP(&accessToken, "access-token", "k", "", "Access token for API and browser read sessions (empty = no auth)")
	pflag.IntVarP(&idleShutdownMs, "idle-shutdown-ms", "s", 0, "Shut down after all clients disconnect for this many milliseconds (0 = disabled)")

	pflag.StringVar(&presentationDir, "presentationDir", ".", "Deprecated: use --presentation-dir instead")
	pflag.StringVar(&presentationIndex, "presentationIndex", "/index.html", "Deprecated: use --presentation-index instead")
	pflag.IntVar(&activeTtlMs, "activeTtlMs", 7200000, "Deprecated: use --active-ttl-ms instead")
	pflag.StringVar(&presentationsDir, "presentationsDir", "presentations", "Deprecated: use --presentations-dir instead")
	pflag.IntVar(&presentationTtlMs, "presentationTtlMs", 86400000, "Deprecated: use --presentation-ttl-ms instead")
	pflag.StringVar(&accessToken, "accessToken", "", "Deprecated: use --access-token instead")
	pflag.IntVar(&idleShutdownMs, "idleShutdownMs", 0, "Deprecated: use --idle-shutdown-ms instead")

	_ = pflag.CommandLine.MarkDeprecated("presentationDir", "use --presentation-dir instead")
	_ = pflag.CommandLine.MarkDeprecated("presentationIndex", "use --presentation-index instead")
	_ = pflag.CommandLine.MarkDeprecated("activeTtlMs", "use --active-ttl-ms instead")
	_ = pflag.CommandLine.MarkDeprecated("presentationsDir", "use --presentations-dir instead")
	_ = pflag.CommandLine.MarkDeprecated("presentationTtlMs", "use --presentation-ttl-ms instead")
	_ = pflag.CommandLine.MarkDeprecated("accessToken", "use --access-token instead")
	_ = pflag.CommandLine.MarkDeprecated("idleShutdownMs", "use --idle-shutdown-ms instead")

	pflag.Parse()

	cfg := notes.ServerConfig{
		Hostname:          hostname,
		Port:              port,
		PresentationDir:   presentationDir,
		PresentationIndex: presentationIndex,
		ActiveTtlMs:       activeTtlMs,
		PresentationsDir:  presentationsDir,
		PresentationTtlMs: presentationTtlMs,
		AccessToken:       accessToken,
		IdleShutdownMs:    idleShutdownMs,
	}

	server := notes.NewServer(cfg)

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
