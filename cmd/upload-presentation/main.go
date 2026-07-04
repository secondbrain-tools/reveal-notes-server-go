package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/pflag"

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
	var filelist string

	pflag.StringVarP(&serverURL, "server-url", "u", "", "Server URL\n  Example: http://host:1947")
	pflag.StringVarP(&name, "name", "n", "", "Presentation slug\n  Inferred from --html-file when omitted.")
	pflag.StringVarP(&sourceDir, "source-dir", "s", "", "Presentation folder\n  Inferred from --html-file's directory when omitted.")
	pflag.StringVarP(&htmlFile, "html-file", "f", "", "Presentation HTML file\n  Required. This is the local deck entry file to package.")
	pflag.StringVarP(&accessToken, "access-token", "k", "", "Bearer token\n  Optional; send when the server is protected.")
	pflag.StringArrayVarP(&ignores, "ignore", "i", nil, "gitignore-style pattern\n  Repeat to exclude extra paths from the archive.")
	pflag.StringVarP(&filelist, "filelist", "l", "", "Filelist path\n  Optional; inferred from a sibling '<name>.filelist.txt'.")

	pflag.StringVar(&sourceDir, "source", "", "Deprecated: use --source-dir instead")
	pflag.StringVar(&htmlFile, "html", "", "Deprecated: use --html-file instead")

	_ = pflag.CommandLine.MarkDeprecated("source", "use --source-dir instead")
	_ = pflag.CommandLine.MarkDeprecated("html", "use --html-file instead")

	pflag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
		pflag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nREQUIRED\n")
		fmt.Fprintf(os.Stderr, "  --server-url : notes server address\n")
		fmt.Fprintf(os.Stderr, "  --html-file  : local HTML file to package and upload\n")
		fmt.Fprintf(os.Stderr, "\nINFERENCE FROM --html-file\n")
		fmt.Fprintf(os.Stderr, "  --name       : basename of --html-file\n")
		fmt.Fprintf(os.Stderr, "  --source-dir : directory containing --html-file\n")
		fmt.Fprintf(os.Stderr, "  --filelist   : sibling '<name>.filelist.txt' if present\n")
	}

	pflag.Parse()

	// Snapshot explicit user values so the summary can show which fields were inferred.
	origName, origSourceDir, origFilelist := name, sourceDir, filelist

	// Derive --name, --source-dir, and --filelist from --html-file when not explicitly set.
	if htmlFile != "" {
		dir := filepath.Dir(htmlFile)
		base := filepath.Base(htmlFile)
		stem := strings.TrimSuffix(base, filepath.Ext(base))

		if name == "" {
			name = stem
		}
		if sourceDir == "" {
			sourceDir = dir
		}
		filelistCandidate := filepath.Join(dir, stem+".filelist.txt")
		if filelist == "" {
			if _, err := os.Stat(filelistCandidate); err == nil {
				filelist = filelistCandidate
			}
		}

		fmt.Fprintf(os.Stderr, "Inferred from --html-file:\n")
		fmt.Fprintf(os.Stderr, "  --html-file   = %s  (%s)\n", htmlFile, "provided")
		fmt.Fprintf(os.Stderr, "  --name        = %s  (%s)\n", name, originTag(name, origName))
		fmt.Fprintf(os.Stderr, "  --source-dir  = %s  (%s)\n", sourceDir, originTag(sourceDir, origSourceDir))
		if filelist != "" {
			fmt.Fprintf(os.Stderr, "  --filelist    = %s  (%s)\n", filelist, originTag(filelist, origFilelist))
		} else if origFilelist == "" {
			fmt.Fprintf(os.Stderr, "  --filelist    = (not found; sibling %s does not exist)\n", filelistCandidate)
		}
	}

	if serverURL == "" || name == "" || sourceDir == "" || htmlFile == "" {
		pflag.Usage()
		os.Exit(2)
	}
	if err := notes.ValidatePresentationName(name); err != nil {
		fatalf("%v", err)
	}

	archive, err := uploader.BuildArchive(uploader.ArchiveOptions{
		SourceDir:      sourceDir,
		HTMLFile:       htmlFile,
		IgnorePatterns: ignores,
		FilelistPath:   filelist,
	})
	if err != nil {
		fatalf("package presentation: %v", err)
	}

	// Compute local archive hash
	localHash := fmt.Sprintf("sha256:%x", sha256.Sum256(archive))

	// Check remote hash to skip if unchanged
	remoteHash, err := uploader.FetchRemoteHash(context.Background(), http.DefaultClient, serverURL, name, accessToken)
	if err != nil {
		fatalf("check remote hash: %v", err)
	}

	if remoteHash != "" && remoteHash == localHash {
		fmt.Printf("Presentation %q is already up-to-date (hash %s). Skipping upload.\n", name, localHash)
		return
	}

	resp, err := uploader.UploadPresentation(context.Background(), http.DefaultClient, serverURL, name, archive, accessToken)
	if err != nil {
		fatalf("upload presentation: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		// FormatUploadFailure diagnoses whether the response came from a
		// front-end HTTP proxy (nginx/Cloudflare/etc.) or the notes server
		// itself, and renders a "Note:" hint when it did.
		fatalf("%s", uploader.FormatUploadFailure(resp, body, len(archive)))
	}

	if _, err := io.Copy(os.Stdout, resp.Body); err != nil {
		fatalf("read response: %v", err)
	}

}

// originTag returns "inferred" when the value was missing on the command line
// (so it must have been derived from --html-file), and "(provided)" otherwise.
// It is used for the human-readable inference summary printed to stderr.
func originTag(value, origValue string) string {
	if value == "" {
		return ""
	}
	if origValue == "" {
		return "inferred"
	}
	return "provided"
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
