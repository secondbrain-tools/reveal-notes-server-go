package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"

	"remote-notes-server/internal/notes"
	"remote-notes-server/internal/uploader"
)

func main() {
	var serverURL string
	var name string
	var sourceDir string
	var htmlFile string
	var accessToken string
	var ignores []string

	flag.StringVar(&serverURL, "server-url", "", "Server URL, e.g. http://host:1947")
	flag.StringVar(&name, "name", "", "Presentation slug/name")
	flag.StringVar(&sourceDir, "source", "", "Presentation folder to package")
	flag.StringVar(&htmlFile, "html", "", "Presentation HTML file path inside the source folder")
	flag.StringVar(&accessToken, "access-token", "", "Optional bearer token for protected servers")
	flag.Func("ignore", "Repeatable gitignore-style ignore pattern", func(value string) error {
		ignores = append(ignores, value)
		return nil
	})
	flag.Parse()

	if serverURL == "" || name == "" || sourceDir == "" || htmlFile == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := notes.ValidatePresentationName(name); err != nil {
		fatalf("%v", err)
	}

	archive, err := uploader.BuildArchive(uploader.ArchiveOptions{
		SourceDir:      sourceDir,
		HTMLFile:       htmlFile,
		IgnorePatterns: ignores,
	})
	if err != nil {
		fatalf("package presentation: %v", err)
	}

	resp, err := uploader.UploadPresentation(context.Background(), http.DefaultClient, serverURL, name, archive, accessToken)
	if err != nil {
		fatalf("upload presentation: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		if len(body) > 0 {
			fatalf("upload failed: %s: %s", resp.Status, body)
		}
		fatalf("upload failed: %s", resp.Status)
	}

	if _, err := io.Copy(os.Stdout, resp.Body); err != nil {
		fatalf("read response: %v", err)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
