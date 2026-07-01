package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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
	var presentationTtl string
	var idleShutdownMs int

	pflag.StringVarP(&hostname, "hostname", "H", "127.0.0.1", "Hostname to bind to")
	pflag.IntVarP(&port, "port", "p", 1947, "Port to listen on")
	pflag.StringVarP(&presentationDir, "presentation-dir", "d", ".", "Directory containing the presentation")
	pflag.StringVarP(&presentationIndex, "presentation-index", "i", "/index.html", "Presentation index file")
	pflag.IntVarP(&activeTtlMs, "active-ttl-ms", "a", 7200000, "Session TTL in milliseconds")
	pflag.StringVarP(&presentationsDir, "presentations-dir", "u", "presentations", "Directory for uploaded presentations")
	pflag.StringVarP(&presentationTtl, "presentation-ttl", "t", "never", "TTL for uploaded presentations (e.g. 24h, 7d, 4h30m, never)")
	pflag.StringVarP(&accessToken, "access-token", "k", "", "Access token for API and browser read sessions (empty = no auth)")
	pflag.IntVarP(&idleShutdownMs, "idle-shutdown-ms", "s", 0, "Shut down after all clients disconnect for this many milliseconds (0 = disabled)")

	pflag.StringVar(&presentationDir, "presentationDir", ".", "Deprecated: use --presentation-dir instead")
	pflag.StringVar(&presentationIndex, "presentationIndex", "/index.html", "Deprecated: use --presentation-index instead")
	pflag.IntVar(&activeTtlMs, "activeTtlMs", 7200000, "Deprecated: use --active-ttl-ms instead")
	pflag.StringVar(&presentationsDir, "presentationsDir", "presentations", "Deprecated: use --presentations-dir instead")
	pflag.StringVar(&accessToken, "accessToken", "", "Deprecated: use --access-token instead")
	pflag.IntVar(&idleShutdownMs, "idleShutdownMs", 0, "Deprecated: use --idle-shutdown-ms instead")

	_ = pflag.CommandLine.MarkDeprecated("presentationDir", "use --presentation-dir instead")
	_ = pflag.CommandLine.MarkDeprecated("presentationIndex", "use --presentation-index instead")
	_ = pflag.CommandLine.MarkDeprecated("activeTtlMs", "use --active-ttl-ms instead")
	_ = pflag.CommandLine.MarkDeprecated("presentationsDir", "use --presentations-dir instead")
	_ = pflag.CommandLine.MarkDeprecated("accessToken", "use --access-token instead")
	_ = pflag.CommandLine.MarkDeprecated("idleShutdownMs", "use --idle-shutdown-ms instead")

	pflag.Parse()

	var err error
	presentationTTL := time.Duration(0)
	presentationTtlFlag := pflag.Lookup("presentation-ttl")
	if presentationTtlFlag != nil && presentationTtlFlag.Changed {
		presentationTTL, err = parseHumanDuration(presentationTtl)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid --presentation-ttl value %q: %v\n", presentationTtl, err)
			os.Exit(2)
		}
	}

	presentationsAbs, err := filepath.Abs(presentationsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve presentations directory %q: %v\n", presentationsDir, err)
		os.Exit(1)
	}
	if flag := pflag.Lookup("presentations-dir"); flag != nil && !flag.Changed {
		fmt.Printf("Using default presentations directory: %s\n", presentationsAbs)
	} else {
		fmt.Printf("Using presentations directory: %s\n", presentationsAbs)
	}
	if presentationTTL > 0 {
		fmt.Printf("Using presentation TTL: %s\n", presentationTTL)
	} else {
		fmt.Println("Using presentation TTL: never")
	}
	if err := os.MkdirAll(presentationsAbs, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "create presentations directory %q: %v\n", presentationsAbs, err)
		os.Exit(1)
	}
	cfg := notes.ServerConfig{
		Hostname:          hostname,
		Port:              port,
		PresentationDir:   presentationDir,
		PresentationIndex: presentationIndex,
		ActiveTtlMs:       activeTtlMs,
		PresentationsDir:  presentationsAbs,
		PresentationTtlMs: int(presentationTTL.Milliseconds()),
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

func parseHumanDuration(input string) (time.Duration, error) {
	input = strings.ToLower(strings.TrimSpace(input))
	input = strings.ReplaceAll(input, " ", "")
	if input == "" {
		return 0, fmt.Errorf("empty duration")
	}
	if input == "never" || input == "none" || input == "off" || input == "disabled" {
		return 0, nil
	}
	if ms, err := strconv.ParseInt(input, 10, 64); err == nil {
		return time.Duration(ms) * time.Millisecond, nil
	}

	var total time.Duration
	for len(input) > 0 {
		n := 0
		for n < len(input) && ((input[n] >= '0' && input[n] <= '9') || input[n] == '.') {
			n++
		}
		if n == 0 {
			return 0, fmt.Errorf("expected number at %q", input)
		}
		numText := input[:n]
		input = input[n:]

		var unit time.Duration
		switch {
		case strings.HasPrefix(input, "ms"):
			unit = time.Millisecond
			input = input[2:]
		case strings.HasPrefix(input, "us"):
			unit = time.Microsecond
			input = input[2:]
		case strings.HasPrefix(input, "µs"):
			unit = time.Microsecond
			input = input[len("µs"):]
		case strings.HasPrefix(input, "ns"):
			unit = time.Nanosecond
			input = input[2:]
		case strings.HasPrefix(input, "d"):
			unit = 24 * time.Hour
			input = input[1:]
		case strings.HasPrefix(input, "h"):
			unit = time.Hour
			input = input[1:]
		case strings.HasPrefix(input, "m"):
			unit = time.Minute
			input = input[1:]
		case strings.HasPrefix(input, "s"):
			unit = time.Second
			input = input[1:]
		default:
			return 0, fmt.Errorf("missing unit after %q", numText)
		}

		value, err := strconv.ParseFloat(numText, 64)
		if err != nil {
			return 0, fmt.Errorf("parse %q: %w", numText, err)
		}
		total += time.Duration(value * float64(unit))
	}
	return total, nil
}
