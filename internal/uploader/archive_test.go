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
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
