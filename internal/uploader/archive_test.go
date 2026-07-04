package uploader

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIgnoreMatcher(t *testing.T) {
	matcher, err := NewIgnoreMatcher([]string{"*.map", "dist/", "!dist/keep.map", `\!literal.txt`, `\#literal.txt`})
	if err != nil {
		t.Fatalf("NewIgnoreMatcher: %v", err)
	}

	tests := []struct {
		name  string
		path  string
		isDir bool
		want  bool
	}{
		{name: "glob matches nested file", path: "assets/site.map", want: true},
		{name: "dir pattern matches directory", path: "dist", isDir: true, want: true},
		{name: "dir pattern matches descendants", path: "dist/app.js", want: true},
		{name: "negated file re-includes", path: "dist/keep.map", want: false},
		{name: "escaped leading bang matches literally", path: "!literal.txt", want: true},
		{name: "escaped leading hash matches literally", path: "#literal.txt", want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := matcher.Ignored(tc.path, tc.isDir)
			if err != nil {
				t.Fatalf("Ignored(%q): %v", tc.path, err)
			}
			if got != tc.want {
				t.Fatalf("Ignored(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestBuildArchiveRenamesHTMLAndPreservesPaths(t *testing.T) {
	source := t.TempDir()
	mustWriteFile(t, filepath.Join(source, "pages", "presentation.html"), []byte("<html><body>deck</body></html>"))
	mustWriteFile(t, filepath.Join(source, "assets", "app.js"), []byte("console.log('ok')"))
	mustWriteFile(t, filepath.Join(source, "assets", "ignored.map"), []byte("map"))
	mustWriteFile(t, filepath.Join(source, "dist", "skip.txt"), []byte("skip"))

	archive, err := BuildArchive(ArchiveOptions{
		SourceDir:      source,
		HTMLFile:       filepath.Join("pages", "presentation.html"),
		IgnorePatterns: []string{"*.map", "dist/"},
	})
	if err != nil {
		t.Fatalf("BuildArchive: %v", err)
	}

	entries := zipToMap(t, archive)
	if got := string(entries["index.html"]); got != "<html><body>deck</body></html>" {
		t.Fatalf("index.html content = %q", got)
	}
	if _, ok := entries["pages/presentation.html"]; ok {
		t.Fatalf("original html path should be renamed, got entries: %v", keys(entries))
	}
	if got := string(entries["assets/app.js"]); got != "console.log('ok')" {
		t.Fatalf("assets/app.js content = %q", got)
	}
	if _, ok := entries["assets/ignored.map"]; ok {
		t.Fatalf("ignored file was packaged")
	}
	if _, ok := entries["dist/skip.txt"]; ok {
		t.Fatalf("ignored directory contents were packaged")
	}
}

func TestBuildArchiveValidation(t *testing.T) {
	t.Run("out of tree html", func(t *testing.T) {
		source := t.TempDir()
		mustWriteFile(t, filepath.Join(source, "index.html"), []byte("ok"))
		outside := filepath.Join(t.TempDir(), "outside.html")
		mustWriteFile(t, outside, []byte("outside"))

		_, err := BuildArchive(ArchiveOptions{SourceDir: source, HTMLFile: outside})
		if err == nil || !strings.Contains(err.Error(), "must be inside source directory") {
			t.Fatalf("expected out-of-tree error, got %v", err)
		}
	})

	t.Run("duplicate output entry", func(t *testing.T) {
		source := t.TempDir()
		mustWriteFile(t, filepath.Join(source, "index.html"), []byte("root"))
		mustWriteFile(t, filepath.Join(source, "pages", "presentation.html"), []byte("nested"))

		_, err := BuildArchive(ArchiveOptions{SourceDir: source, HTMLFile: filepath.Join("pages", "presentation.html")})
		if err == nil || !strings.Contains(err.Error(), "duplicate zip entry \"index.html\"") {
			t.Fatalf("expected duplicate entry error, got %v", err)
		}
	})

	t.Run("rename conflicts with existing directory", func(t *testing.T) {
		source := t.TempDir()
		mustWriteFile(t, filepath.Join(source, "index.html", "nested.txt"), []byte("nested"))
		mustWriteFile(t, filepath.Join(source, "pages", "presentation.html"), []byte("presentation"))

		_, err := BuildArchive(ArchiveOptions{SourceDir: source, HTMLFile: filepath.Join("pages", "presentation.html")})
		if err == nil || !strings.Contains(err.Error(), "conflicts with existing") {
			t.Fatalf("expected conflict error, got %v", err)
		}
	})
}

func TestBuildArchiveResolvesHTMLFileRelativeToCWDWhenNeeded(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root := t.TempDir()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	source := filepath.Join(root, "notes-client")
	mustWriteFile(t, filepath.Join(source, "demo.html"), []byte("<html><body>deck</body></html>"))
	mustWriteFile(t, filepath.Join(source, "app.js"), []byte("console.log('ok')"))

	archive, err := BuildArchive(ArchiveOptions{
		SourceDir: source,
		HTMLFile:  filepath.Join("notes-client", "demo.html"),
	})
	if err != nil {
		t.Fatalf("BuildArchive: %v", err)
	}

	entries := zipToMap(t, archive)
	if got := string(entries["index.html"]); got != "<html><body>deck</body></html>" {
		t.Fatalf("index.html content = %q", got)
	}
	if _, ok := entries["app.js"]; !ok {
		t.Fatal("expected app.js in archive")
	}
}

func TestBuildUploadRequest(t *testing.T) {
	t.Run("with bearer token", func(t *testing.T) {
		req, err := BuildUploadRequest(context.Background(), "http://example.test/base/", "my-talk", []byte("zipdata"), "secret")
		if err != nil {
			t.Fatalf("BuildUploadRequest: %v", err)
		}
		if req.Method != http.MethodPost {
			t.Fatalf("method = %s", req.Method)
		}
		if got := req.URL.Path; got != "/base/api/presentations/my-talk" {
			t.Fatalf("url path = %q", got)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("authorization header = %q", got)
		}
		field, filename, data := readMultipartUpload(t, req)
		if field != "file" {
			t.Fatalf("multipart field = %q", field)
		}
		if filename != "my-talk.zip" {
			t.Fatalf("multipart filename = %q", filename)
		}
		if string(data) != "zipdata" {
			t.Fatalf("multipart data = %q", string(data))
		}
	})

	t.Run("without token", func(t *testing.T) {
		req, err := BuildUploadRequest(context.Background(), "http://example.test", "my-talk", []byte("zipdata"), "")
		if err != nil {
			t.Fatalf("BuildUploadRequest: %v", err)
		}
		if got := req.Header.Get("Authorization"); got != "" {
			t.Fatalf("unexpected authorization header = %q", got)
		}
	})
}

func readMultipartUpload(t *testing.T, req *http.Request) (fieldName, filename string, data []byte) {
	t.Helper()

	contentType := req.Header.Get("Content-Type")
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatalf("parse media type: %v", err)
	}
	boundary := params["boundary"]
	if boundary == "" {
		t.Fatal("missing multipart boundary")
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	part, err := reader.NextPart()
	if err != nil {
		t.Fatalf("next part: %v", err)
	}
	defer part.Close()

	data, err = io.ReadAll(part)
	if err != nil {
		t.Fatalf("read part: %v", err)
	}
	return part.FormName(), part.FileName(), data
}

func mustWriteFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write file %s: %v", path, err)
	}
}

func zipToMap(t *testing.T, data []byte) map[string][]byte {
	t.Helper()

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	entries := map[string][]byte{}
	for _, file := range zr.File {
		if file.FileInfo().IsDir() {
			entries[file.Name] = nil
			continue
		}
		rc, err := file.Open()
		if err != nil {
			t.Fatalf("open zip entry %s: %v", file.Name, err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read zip entry %s: %v", file.Name, err)
		}
		entries[file.Name] = data
	}
	return entries
}

func keys(m map[string][]byte) []string {
	s := make([]string, 0, len(m))
	for k := range m {
		s = append(s, k)
	}
	return s
}
func TestParseFilelist(t *testing.T) {
	t.Run("empty filelist", func(t *testing.T) {
		f := mustWriteFilelist(t, "")
		patterns, err := parseFilelist(f)
		if err != nil {
			t.Fatalf("parseFilelist: %v", err)
		}
		if len(patterns) != 0 {
			t.Fatalf("expected 0 patterns, got %d: %v", len(patterns), patterns)
		}
	})

	t.Run("comments and blanks ignored", func(t *testing.T) {
		content := `# This is a comment

*.js

  # indented comment

assets/
`
		f := mustWriteFilelist(t, content)
		patterns, err := parseFilelist(f)
		if err != nil {
			t.Fatalf("parseFilelist: %v", err)
		}
		if len(patterns) != 2 {
			t.Fatalf("expected 2 patterns, got %d: %v", len(patterns), patterns)
		}
		if patterns[0] != "*.js" {
			t.Fatalf("pattern[0] = %q, want %q", patterns[0], "*.js")
		}
		if patterns[1] != "assets/" {
			t.Fatalf("pattern[1] = %q, want %q", patterns[1], "assets/")
		}
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := parseFilelist("/nonexistent/filelist.txt")
		if err == nil {
			t.Fatal("expected error for missing file")
		}
	})

	t.Run("whitespace trimming", func(t *testing.T) {
		content := `  *.js  
	assets/  
`
		f := mustWriteFilelist(t, content)
		patterns, err := parseFilelist(f)
		if err != nil {
			t.Fatalf("parseFilelist: %v", err)
		}
		if len(patterns) != 2 {
			t.Fatalf("expected 2 patterns, got %d: %v", len(patterns), patterns)
		}
		if patterns[0] != "*.js" {
			t.Fatalf("pattern[0] = %q, want %q", patterns[0], "*.js")
		}
		if patterns[1] != "assets/" {
			t.Fatalf("pattern[1] = %q, want %q", patterns[1], "assets/")
		}
	})
}

func TestBuildArchiveFilelist(t *testing.T) {
	t.Run("filelist with specific files", func(t *testing.T) {
		source := t.TempDir()
		mustWriteFile(t, filepath.Join(source, "presentation.html"), []byte("<html></html>"))
		mustWriteFile(t, filepath.Join(source, "app.js"), []byte("console.log('js')"))
		mustWriteFile(t, filepath.Join(source, "style.css"), []byte("body {}"))
		mustWriteFile(t, filepath.Join(source, "ignored.txt"), []byte("should not appear"))

		filelist := mustWriteFilelist(t, "*.js\n*.css\n")

		archive, err := BuildArchive(ArchiveOptions{
			SourceDir:    source,
			HTMLFile:     "presentation.html",
			FilelistPath: filelist,
		})
		if err != nil {
			t.Fatalf("BuildArchive: %v", err)
		}

		entries := zipToMap(t, archive)
		if _, ok := entries["index.html"]; !ok {
			t.Fatal("expected index.html in archive")
		}
		if _, ok := entries["app.js"]; !ok {
			t.Fatal("expected app.js in archive")
		}
		if _, ok := entries["style.css"]; !ok {
			t.Fatal("expected style.css in archive")
		}
		if _, ok := entries["ignored.txt"]; ok {
			t.Fatal("ignored.txt should NOT be in archive")
		}
	})

	t.Run("filelist with directory", func(t *testing.T) {
		source := t.TempDir()
		mustWriteFile(t, filepath.Join(source, "presentation.html"), []byte("<html></html>"))
		mustWriteFile(t, filepath.Join(source, "assets", "app.js"), []byte("js"))
		mustWriteFile(t, filepath.Join(source, "assets", "sub", "deep.js"), []byte("deep"))
		mustWriteFile(t, filepath.Join(source, "dist", "bundle.js"), []byte("bundle"))

		filelist := mustWriteFilelist(t, "assets/\n")

		archive, err := BuildArchive(ArchiveOptions{
			SourceDir:    source,
			HTMLFile:     "presentation.html",
			FilelistPath: filelist,
		})
		if err != nil {
			t.Fatalf("BuildArchive: %v", err)
		}

		entries := zipToMap(t, archive)
		if _, ok := entries["index.html"]; !ok {
			t.Fatal("expected index.html in archive")
		}
		if _, ok := entries["assets/app.js"]; !ok {
			t.Fatal("expected assets/app.js in archive")
		}
		if _, ok := entries["assets/sub/deep.js"]; !ok {
			t.Fatal("expected assets/sub/deep.js in archive")
		}
		if _, ok := entries["dist/bundle.js"]; ok {
			t.Fatal("dist/bundle.js should NOT be in archive")
		}
	})

	t.Run("filelist with glob pattern", func(t *testing.T) {
		source := t.TempDir()
		mustWriteFile(t, filepath.Join(source, "presentation.html"), []byte("<html></html>"))
		mustWriteFile(t, filepath.Join(source, "assets", "app.js"), []byte("js"))
		mustWriteFile(t, filepath.Join(source, "nested", "deep.js"), []byte("deep"))
		mustWriteFile(t, filepath.Join(source, "style.css"), []byte("css"))

		filelist := mustWriteFilelist(t, "**/*.js\n")

		archive, err := BuildArchive(ArchiveOptions{
			SourceDir:    source,
			HTMLFile:     "presentation.html",
			FilelistPath: filelist,
		})
		if err != nil {
			t.Fatalf("BuildArchive: %v", err)
		}

		entries := zipToMap(t, archive)
		if _, ok := entries["index.html"]; !ok {
			t.Fatal("expected index.html in archive")
		}
		if _, ok := entries["assets/app.js"]; !ok {
			t.Fatal("expected assets/app.js in archive")
		}
		if _, ok := entries["nested/deep.js"]; !ok {
			t.Fatal("expected nested/deep.js in archive")
		}
		if _, ok := entries["style.css"]; ok {
			t.Fatal("style.css should NOT be in archive")
		}
	})

	t.Run("filelist with negation", func(t *testing.T) {
		source := t.TempDir()
		mustWriteFile(t, filepath.Join(source, "presentation.html"), []byte("<html></html>"))
		mustWriteFile(t, filepath.Join(source, "assets", "keep.js"), []byte("keep"))
		mustWriteFile(t, filepath.Join(source, "assets", "skip.js"), []byte("skip"))
		mustWriteFile(t, filepath.Join(source, "dist", "file.js"), []byte("included"))

		filelist := mustWriteFilelist(t, "**/*.js\n!assets/skip.js\n")

		archive, err := BuildArchive(ArchiveOptions{
			SourceDir:    source,
			HTMLFile:     "presentation.html",
			FilelistPath: filelist,
		})
		if err != nil {
			t.Fatalf("BuildArchive: %v", err)
		}

		entries := zipToMap(t, archive)
		if _, ok := entries["index.html"]; !ok {
			t.Fatal("expected index.html in archive")
		}
		if _, ok := entries["assets/keep.js"]; !ok {
			t.Fatal("expected assets/keep.js in archive")
		}
		if _, ok := entries["dist/file.js"]; !ok {
			t.Fatal("expected dist/file.js in archive")
		}
		if _, ok := entries["assets/skip.js"]; ok {
			t.Fatal("assets/skip.js should NOT be in archive")
		}
	})

	t.Run("html force-include even when not in filelist", func(t *testing.T) {
		source := t.TempDir()
		mustWriteFile(t, filepath.Join(source, "presentation.html"), []byte("<html></html>"))
		mustWriteFile(t, filepath.Join(source, "app.js"), []byte("js"))
		mustWriteFile(t, filepath.Join(source, "extra.txt"), []byte("extra"))

		// Only include *.js files
		filelist := mustWriteFilelist(t, "*.js\n")

		archive, err := BuildArchive(ArchiveOptions{
			SourceDir:    source,
			HTMLFile:     "presentation.html",
			FilelistPath: filelist,
		})
		if err != nil {
			t.Fatalf("BuildArchive: %v", err)
		}

		entries := zipToMap(t, archive)
		// HTML file should be included even though it's not *.js
		if _, ok := entries["index.html"]; !ok {
			t.Fatal("expected index.html (force-included) in archive")
		}
		if got := string(entries["index.html"]); got != "<html></html>" {
			t.Fatalf("index.html content = %q", got)
		}
		if _, ok := entries["app.js"]; !ok {
			t.Fatal("expected app.js in archive")
		}
		if _, ok := entries["extra.txt"]; ok {
			t.Fatal("extra.txt should NOT be in archive")
		}
	})

	t.Run("filelist with ignore post-filter", func(t *testing.T) {
		source := t.TempDir()
		mustWriteFile(t, filepath.Join(source, "presentation.html"), []byte("<html></html>"))
		mustWriteFile(t, filepath.Join(source, "assets", "app.js"), []byte("js"))
		mustWriteFile(t, filepath.Join(source, "assets", "app.map"), []byte("map"))

		// Filelist includes everything under assets/
		// --ignore excludes *.map files
		filelist := mustWriteFilelist(t, "assets/\n")

		archive, err := BuildArchive(ArchiveOptions{
			SourceDir:      source,
			HTMLFile:       "presentation.html",
			FilelistPath:   filelist,
			IgnorePatterns: []string{"*.map"},
		})
		if err != nil {
			t.Fatalf("BuildArchive: %v", err)
		}

		entries := zipToMap(t, archive)
		if _, ok := entries["index.html"]; !ok {
			t.Fatal("expected index.html in archive")
		}
		if _, ok := entries["assets/app.js"]; !ok {
			t.Fatal("expected assets/app.js in archive")
		}
		if _, ok := entries["assets/app.map"]; ok {
			t.Fatal("assets/app.map should be excluded by --ignore")
		}
	})

	t.Run("empty filelist includes only html", func(t *testing.T) {
		source := t.TempDir()
		mustWriteFile(t, filepath.Join(source, "presentation.html"), []byte("<html></html>"))
		mustWriteFile(t, filepath.Join(source, "app.js"), []byte("js"))

		// Empty filelist — no patterns, so nothing matches
		filelist := mustWriteFilelist(t, "")

		archive, err := BuildArchive(ArchiveOptions{
			SourceDir:    source,
			HTMLFile:     "presentation.html",
			FilelistPath: filelist,
		})
		if err != nil {
			t.Fatalf("BuildArchive: %v", err)
		}

		entries := zipToMap(t, archive)
		if _, ok := entries["index.html"]; !ok {
			t.Fatal("expected index.html in archive")
		}
		if len(entries) != 1 {
			t.Fatalf("expected only index.html, got %d entries: %v", len(entries), keys(entries))
		}
	})

	t.Run("missing filelist returns error", func(t *testing.T) {
		source := t.TempDir()
		mustWriteFile(t, filepath.Join(source, "presentation.html"), []byte("<html></html>"))

		_, err := BuildArchive(ArchiveOptions{
			SourceDir:    source,
			HTMLFile:     "presentation.html",
			FilelistPath: filepath.Join(source, "nonexistent.txt"),
		})
		if err == nil {
			t.Fatal("expected error for missing filelist")
		}
		if !strings.Contains(err.Error(), "read filelist") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("filelist patterns interact with subdirectories", func(t *testing.T) {
		source := t.TempDir()
		mustWriteFile(t, filepath.Join(source, "presentation.html"), []byte("<html></html>"))
		mustWriteFile(t, filepath.Join(source, "assets", "js", "main.js"), []byte("main"))
		mustWriteFile(t, filepath.Join(source, "assets", "css", "style.css"), []byte("style"))
		mustWriteFile(t, filepath.Join(source, "vendor", "lib.js"), []byte("lib"))

		// Include all JS files anywhere via **/ pattern
		filelist := mustWriteFilelist(t, "**/*.js\n")

		archive, err := BuildArchive(ArchiveOptions{
			SourceDir:    source,
			HTMLFile:     "presentation.html",
			FilelistPath: filelist,
		})
		if err != nil {
			t.Fatalf("BuildArchive: %v", err)
		}

		entries := zipToMap(t, archive)
		if _, ok := entries["index.html"]; !ok {
			t.Fatal("expected index.html in archive")
		}
		if _, ok := entries["assets/js/main.js"]; !ok {
			t.Fatal("expected assets/js/main.js in archive")
		}
		if _, ok := entries["vendor/lib.js"]; !ok {
			t.Fatal("expected vendor/lib.js in archive")
		}
		if _, ok := entries["assets/css/style.css"]; ok {
			t.Fatal("style.css should NOT be in archive")
		}
	})
}

func TestBuildArchiveFilelistSymlinkRejected(t *testing.T) {
	source := t.TempDir()
	mustWriteFile(t, filepath.Join(source, "presentation.html"), []byte("<html></html>"))
	mustWriteFile(t, filepath.Join(source, "real.txt"), []byte("real"))
	if err := os.Symlink("real.txt", filepath.Join(source, "link.txt")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	filelist := mustWriteFilelist(t, "**/*.txt\n")

	_, err := BuildArchive(ArchiveOptions{
		SourceDir:    source,
		HTMLFile:     "presentation.html",
		FilelistPath: filelist,
	})
	if err == nil || !strings.Contains(err.Error(), "symlinks are not supported") {
		t.Fatalf("expected symlink error, got %v", err)
	}
}

// TestBuildArchiveFilelistExcludesSymlinkedTree covers the regression where a
// subtree not referenced by the filelist (e.g. a sibling WebApps/ project that
// happens to sit next to a presentation and contains node_modules/.pnpm/...
// symlinks) must not trip the symlink guard. The walker descends into the
// excluded tree but every entry is filtered out by the include matcher
// before the symlink check runs.
func TestBuildArchiveFilelistExcludesSymlinkedTree(t *testing.T) {
	source := t.TempDir()
	mustWriteFile(t, filepath.Join(source, "presentation.html"), []byte("<html></html>"))
	mustWriteFile(t, filepath.Join(source, "plugin", "main.js"), []byte("plugin"))

	// Mirror a pnpm hoisted-store layout: a real file under .pnpm/... and a
	// sibling node_modules/<dep> symlink pointing into it.
	pnpmDir := filepath.Join(source, "WebApps", "code", "basic-project", "node_modules", ".pnpm", "@babel+code-frame@7.29.0", "node_modules", "@babel", "code-frame")
	if err := os.MkdirAll(filepath.Dir(pnpmDir), 0o755); err != nil {
		t.Fatalf("mkdir pnpm parent: %v", err)
	}
	mustWriteFile(t, pnpmDir, []byte("code-frame"))
	linkPath := filepath.Join(filepath.Dir(filepath.Dir(pnpmDir)), "helper-validator-identifier")
	if err := os.Symlink("../../../@babel+helper-validator-identifier@7.28.5/node_modules/@babel/helper-validator-identifier", linkPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	// Filelist only mentions plugin/, nothing under WebApps/.
	filelist := mustWriteFilelist(t, "plugin/\n")

	archive, err := BuildArchive(ArchiveOptions{
		SourceDir:    source,
		HTMLFile:     "presentation.html",
		FilelistPath: filelist,
	})
	if err != nil {
		t.Fatalf("BuildArchive: %v", err)
	}

	entries := zipToMap(t, archive)
	if _, ok := entries["index.html"]; !ok {
		t.Fatal("expected index.html in archive")
	}
	if _, ok := entries["plugin/main.js"]; !ok {
		t.Fatal("expected plugin/main.js in archive")
	}
	for name := range entries {
		if strings.HasPrefix(name, "WebApps/") {
			t.Fatalf("excluded WebApps tree leaked into archive: %s", name)
		}
	}
}

// TestBuildArchiveIgnoreExcludesSymlinkedTree covers the same regression but
// with the exclusion coming from --ignore rather than the filelist. A user
// passing `--ignore WebApps/` must get a successful build even when that tree
// contains symlinks.
func TestBuildArchiveIgnoreExcludesSymlinkedTree(t *testing.T) {
	source := t.TempDir()
	mustWriteFile(t, filepath.Join(source, "presentation.html"), []byte("<html></html>"))
	mustWriteFile(t, filepath.Join(source, "plugin", "main.js"), []byte("plugin"))

	pnpmDir := filepath.Join(source, "WebApps", "code", "basic-project", "node_modules", ".pnpm", "@babel+code-frame@7.29.0", "node_modules", "@babel", "code-frame")
	if err := os.MkdirAll(filepath.Dir(pnpmDir), 0o755); err != nil {
		t.Fatalf("mkdir pnpm parent: %v", err)
	}
	mustWriteFile(t, pnpmDir, []byte("code-frame"))
	linkPath := filepath.Join(filepath.Dir(filepath.Dir(pnpmDir)), "helper-validator-identifier")
	if err := os.Symlink("../../../@babel+helper-validator-identifier@7.28.5/node_modules/@babel/helper-validator-identifier", linkPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	archive, err := BuildArchive(ArchiveOptions{
		SourceDir:      source,
		HTMLFile:       "presentation.html",
		IgnorePatterns: []string{"WebApps/"},
	})
	if err != nil {
		t.Fatalf("BuildArchive: %v", err)
	}

	entries := zipToMap(t, archive)
	if _, ok := entries["index.html"]; !ok {
		t.Fatal("expected index.html in archive")
	}
	if _, ok := entries["plugin/main.js"]; !ok {
		t.Fatal("expected plugin/main.js in archive")
	}
	for name := range entries {
		if strings.HasPrefix(name, "WebApps/") {
			t.Fatalf("excluded WebApps tree leaked into archive: %s", name)
		}
	}
}

// mustWriteFilelist writes content to a temp file and returns its path.
func mustWriteFilelist(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "filelist-*.txt")
	if err != nil {
		t.Fatalf("create temp filelist: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write filelist: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close filelist: %v", err)
	}
	return f.Name()
}
