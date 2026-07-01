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

pflag.StringVarP(&serverURL, "server-url", "u", "", "Server URL, e.g. http://host:1947 (required)")
pflag.StringVarP(&name, "name", "n", "", "Presentation slug/name. Inferred from --html-file basename (extension stripped) when --html-file is set and this is omitted.")
pflag.StringVarP(&sourceDir, "source-dir", "s", "", "Presentation folder to package. Inferred from --html-file's directory when --html-file is set and this is omitted.")
pflag.StringVarP(&htmlFile, "html-file", "f", "", "Presentation HTML file path (required). When set, --name, --source-dir, and a sibling --filelist can be inferred from it.")
	pflag.StringVarP(&accessToken, "access-token", "k", "", "Optional bearer token for protected servers")
	pflag.StringArrayVarP(&ignores, "ignore", "i", nil, "Repeatable gitignore-style ignore pattern")
pflag.StringVarP(&filelist, "filelist", "l", "", "Optional filelist file defining which relative paths to include. Inferred as '<name>.filelist.txt' next to --html-file when --html-file is set, this is omitted, and the sibling file exists.")

	pflag.StringVar(&sourceDir, "source", "", "Deprecated: use --source-dir instead")
	pflag.StringVar(&htmlFile, "html", "", "Deprecated: use --html-file instead")

	_ = pflag.CommandLine.MarkDeprecated("source", "use --source-dir instead")
	_ = pflag.CommandLine.MarkDeprecated("html", "use --html-file instead")

pflag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
		pflag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nINFERENCE FROM --html-file\n")
		fmt.Fprintf(os.Stderr, "  When --html-file is set, the following flags can be omitted and will be\n")
		fmt.Fprintf(os.Stderr, "  derived from its path. Explicit values always win over inference.\n")
		fmt.Fprintf(os.Stderr, "\n")
		fmt.Fprintf(os.Stderr, "    --name       ← basename of --html-file with its extension stripped\n")
		fmt.Fprintf(os.Stderr, "                  e.g. --html-file=…/my-talk.html → --name=my-talk\n")
		fmt.Fprintf(os.Stderr, "    --source-dir ← directory containing --html-file\n")
		fmt.Fprintf(os.Stderr, "                  e.g. --html-file=…/out/my-talk.html → --source-dir=…/out\n")
		fmt.Fprintf(os.Stderr, "    --filelist   ← '<name>.filelist.txt' sibling of --html-file, used only\n")
		fmt.Fprintf(os.Stderr, "                  when that file exists. Missing siblings are reported as\n")
		fmt.Fprintf(os.Stderr, "                  '(not found)' in the inference summary printed to stderr.\n")
		fmt.Fprintf(os.Stderr, "\n")
		fmt.Fprintf(os.Stderr, "  Example (full inference from --html-file only):\n")
		fmt.Fprintf(os.Stderr, "    %s --server-url=http://host:1947 \\\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "      --html-file=…/out/my-talk.html\n")
		fmt.Fprintf(os.Stderr, "\n")
		fmt.Fprintf(os.Stderr, "  The actual resolved values (with origin tags) are printed to stderr at\n")
		fmt.Fprintf(os.Stderr, "  startup so you can verify the inference before the upload runs.\n")
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
		if len(body) > 0 {
			fatalf("upload failed: %s: %s", resp.Status, body)
		}
		fatalf("upload failed: %s", resp.Status)
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
