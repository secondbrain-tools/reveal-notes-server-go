package uploader

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type ArchiveOptions struct {
	SourceDir      string
	HTMLFile       string
	IgnorePatterns []string
	FilelistPath   string
}

func BuildArchive(opts ArchiveOptions) (archive []byte, err error) {
	if opts.SourceDir == "" {
		return nil, fmt.Errorf("source directory is required")
	}
	if opts.HTMLFile == "" {
		return nil, fmt.Errorf("html file path is required")
	}

	sourceAbs, err := filepath.Abs(opts.SourceDir)
	if err != nil {
		return nil, fmt.Errorf("resolve source directory: %w", err)
	}
	sourceAbs = filepath.Clean(sourceAbs)
	if stat, err := os.Stat(sourceAbs); err != nil {
		return nil, fmt.Errorf("source directory %q: %w", opts.SourceDir, err)
	} else if !stat.IsDir() {
		return nil, fmt.Errorf("source directory %q is not a directory", opts.SourceDir)
	}

	htmlAbs, err := resolveHTMLFilePath(sourceAbs, opts.HTMLFile)
	if err != nil {
		return nil, err
	}
	htmlAbs = filepath.Clean(htmlAbs)
	htmlInfo, err := os.Lstat(htmlAbs)
	if err != nil {
		return nil, fmt.Errorf("html file %q: %w", opts.HTMLFile, err)
	}
	if htmlInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("html file %q must not be a symlink", opts.HTMLFile)
	}
	if htmlInfo.IsDir() {
		return nil, fmt.Errorf("html file %q is a directory", opts.HTMLFile)
	}

	htmlRel, err := filepath.Rel(sourceAbs, htmlAbs)
	if err != nil || htmlRel == ".." || strings.HasPrefix(htmlRel, ".."+string(os.PathSeparator)) {
		return nil, fmt.Errorf("html file %q must be inside source directory %q", opts.HTMLFile, opts.SourceDir)
	}

	matcher, err := NewIgnoreMatcher(opts.IgnorePatterns)
	if err != nil {
		return nil, err
	}
	if ignored, err := matcher.Ignored(filepath.ToSlash(htmlRel), false); err != nil {
		return nil, err
	} else if ignored {
		return nil, fmt.Errorf("selected html file %q is ignored by --ignore", opts.HTMLFile)
	}

	// Parse filelist if provided
	var includeMatcher *IgnoreMatcher
	if opts.FilelistPath != "" {
		patterns, err := parseFilelist(opts.FilelistPath)
		if err != nil {
			return nil, err
		}
		includeMatcher, err = NewIgnoreMatcher(patterns)
		if err != nil {
			return nil, fmt.Errorf("filelist %q: %w", opts.FilelistPath, err)
		}
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	tracker := newZipEntryTracker()

	walkErr := filepath.WalkDir(sourceAbs, func(current string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if current == sourceAbs {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks are not supported: %s", current)
		}

		rel, err := filepath.Rel(sourceAbs, current)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		// Filelist include check: only include paths matching filelist patterns
		if includeMatcher != nil && current != htmlAbs {
			included, err := includeMatcher.Ignored(relSlash, d.IsDir())
			if err != nil {
				return err
			}
			if !included {
				return nil
			}
		}

		// Apply --ignore patterns as post-filter
		ignored, err := matcher.Ignored(relSlash, d.IsDir())
		if err != nil {
			return err
		}
		if ignored {
			return nil
		}

		entryName := relSlash
		entryKind := zipEntryFile
		if d.IsDir() {
			entryName += "/"
			entryKind = zipEntryDir
		} else if current == htmlAbs {
			entryName = "index.html"
		}

		if err := tracker.add(entryName, entryKind); err != nil {
			return err
		}

		if d.IsDir() {
			_, err := zw.Create(entryName)
			return err
		}

		f, err := os.Open(current)
		if err != nil {
			return err
		}

		w, err := zw.Create(entryName)
		if err != nil {
			_ = f.Close()
			return err
		}
		if _, err := io.Copy(w, f); err != nil {
			_ = f.Close()
			return err
		}
		return f.Close()
	})
	if walkErr != nil {
		_ = zw.Close()
		return nil, walkErr
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// parseFilelist reads a filelist file and returns the list of non-empty,
// non-comment patterns. Lines starting with "#" are treated as comments.
func parseFilelist(filelistPath string) ([]string, error) {
	data, err := os.ReadFile(filelistPath)
	if err != nil {
		return nil, fmt.Errorf("read filelist %q: %w", filelistPath, err)
	}
	var patterns []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns, nil
}

func resolveHTMLFilePath(sourceAbs, htmlFile string) (string, error) {
	if filepath.IsAbs(htmlFile) {
		return htmlFile, nil
	}

	candidate := filepath.Join(sourceAbs, htmlFile)
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}

	abs, err := filepath.Abs(htmlFile)
	if err != nil {
		return "", fmt.Errorf("resolve html file: %w", err)
	}
	return abs, nil
}

type zipEntryKind int

const (
	zipEntryDir zipEntryKind = iota
	zipEntryFile
)

type zipEntryTracker struct {
	entries map[string]zipEntryKind
}

func newZipEntryTracker() *zipEntryTracker {
	return &zipEntryTracker{entries: make(map[string]zipEntryKind)}
}

func (t *zipEntryTracker) add(entryName string, kind zipEntryKind) error {
	cleaned := strings.TrimSuffix(entryName, "/")
	if cleaned == "" {
		return fmt.Errorf("invalid zip entry %q", entryName)
	}

	if existingKind, ok := t.entries[cleaned]; ok {
		if existingKind == kind {
			return fmt.Errorf("duplicate zip entry %q", entryName)
		}
		if kind == zipEntryFile {
			return fmt.Errorf("zip entry %q conflicts with existing directory %q", entryName, cleaned)
		}
		return fmt.Errorf("zip entry %q conflicts with existing file %q", entryName, cleaned)
	}

	for existingPath, existingKind := range t.entries {
		switch kind {
		case zipEntryFile:
			if pathHasPrefix(existingPath, cleaned) {
				if existingKind == zipEntryDir {
					return fmt.Errorf("zip entry %q conflicts with existing directory %q", entryName, existingPath)
				}
				return fmt.Errorf("zip entry %q conflicts with existing file %q", entryName, existingPath)
			}
			if existingKind == zipEntryFile && pathHasPrefix(cleaned, existingPath) {
				return fmt.Errorf("zip entry %q conflicts with existing file %q", entryName, existingPath)
			}
		case zipEntryDir:
			if existingKind == zipEntryFile && pathHasPrefix(cleaned, existingPath) {
				return fmt.Errorf("zip entry %q conflicts with existing file %q", entryName, existingPath)
			}
		}
	}

	t.entries[cleaned] = kind
	return nil
}

func pathHasPrefix(value, prefix string) bool {
	return value == prefix || strings.HasPrefix(value, prefix+"/")
}
