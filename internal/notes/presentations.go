package notes

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// validPresentationName matches alphanumeric names with dots, underscores, hyphens.
// Must start with a letter or digit, max 64 characters.
var validPresentationName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

// requireAccessToken returns a middleware that checks the Authorization header.
// If the token is empty, the check is skipped (no auth required).
func requireAccessToken(token string, next http.HandlerFunc) http.HandlerFunc {
	if token == "" {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		expected := "Bearer " + token
		if auth != expected {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		next(w, r)
	}
}

// Presentation represents an uploaded presentation.
type Presentation struct {
	Name      string    `json:"name"`
	Path      string    `json:"-"`
	CreatedAt time.Time `json:"createdAt"`
	Size      int64     `json:"size"`
}

// PresentationInfo is the JSON-friendly representation for the list endpoint.
type PresentationInfo struct {
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
	Size      int64  `json:"size"`
	SizeHuman string `json:"sizeHuman"`
}

// PresentationStore manages uploaded presentations on disk.
type PresentationStore struct {
	mu       sync.RWMutex
	dir      string
	ttl      time.Duration
	now      func() time.Time
	items    map[string]*Presentation
}

// NewPresentationStore creates a new PresentationStore rooted at dir.
func NewPresentationStore(dir string, ttl time.Duration) *PresentationStore {
	os.MkdirAll(dir, 0755)
	return &PresentationStore{
		dir:   dir,
		ttl:   ttl,
		now:   time.Now,
		items: make(map[string]*Presentation),
	}
}

// Add extracts a zip archive from r into the presentation directory named name.
// If a presentation with the same name already exists, it is replaced.
func (ps *PresentationStore) Add(name string, r io.Reader) (*Presentation, error) {
	if !validPresentationName.MatchString(name) {
		return nil, fmt.Errorf("invalid presentation name: %q", name)
	}

	ps.mu.Lock()
	defer ps.mu.Unlock()

	dest := filepath.Join(ps.dir, name)

	// Remove existing if present
	if err := os.RemoveAll(dest); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove old presentation: %w", err)
	}

	// Create temp file for the zip (archive/zip needs seekable reader)
	tmpFile, err := os.CreateTemp("", "pres-upload-*.zip")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	written, err := io.Copy(tmpFile, r)
	if err != nil {
		return nil, fmt.Errorf("write temp zip: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return nil, fmt.Errorf("close temp zip: %w", err)
	}

	zipReader, err := zip.OpenReader(tmpFile.Name())
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	defer zipReader.Close()

	// Extract files with zip-slip protection
	var totalSize int64
	for _, f := range zipReader.File {
		// Clean the path and check for traversal
		cleanPath := filepath.Clean(f.Name)
		if strings.HasPrefix(cleanPath, ".."+string(filepath.Separator)) || cleanPath == ".." {
			continue
		}

		target := filepath.Join(dest, cleanPath)
		if !strings.HasPrefix(target, filepath.Clean(dest)+string(filepath.Separator)) {
			continue
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(target, 0755)
			continue
		}

		// Ensure parent directory exists
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return nil, fmt.Errorf("create dir %s: %w", filepath.Dir(target), err)
		}

		src, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("open zip entry %s: %w", f.Name, err)
		}

		dst, err := os.Create(target)
		if err != nil {
			src.Close()
			return nil, fmt.Errorf("create file %s: %w", target, err)
		}

		n, err := io.Copy(dst, src)
		src.Close()
		dst.Close()
		if err != nil {
			return nil, fmt.Errorf("extract %s: %w", f.Name, err)
		}
		totalSize += n
	}

	now := ps.now()
	pres := &Presentation{
		Name:      name,
		Path:      dest,
		CreatedAt: now,
		Size:      written, // compressed size
	}
	ps.items[name] = pres

	return pres, nil
}

// Get returns a presentation by name, or nil if not found.
func (ps *PresentationStore) Get(name string) *Presentation {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return ps.items[name]
}

// Remove deletes a presentation from disk and memory.
func (ps *PresentationStore) Remove(name string) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	pres, ok := ps.items[name]
	if !ok {
		return fmt.Errorf("presentation %q not found", name)
	}

	if err := os.RemoveAll(pres.Path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove presentation files: %w", err)
	}

	delete(ps.items, name)
	return nil
}

// List returns a copy of all presentations sorted by CreatedAt descending.
func (ps *PresentationStore) List() []PresentationInfo {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	result := make([]PresentationInfo, 0, len(ps.items))
	for _, p := range ps.items {
		result = append(result, PresentationInfo{
			Name:      p.Name,
			CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339),
			Size:      p.Size,
			SizeHuman: formatBytes(p.Size),
		})
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt > result[j].CreatedAt
	})

	return result
}

// Prune removes presentations older than the store's TTL.
func (ps *PresentationStore) Prune() {
	cutoff := ps.now().Add(-ps.ttl)

	ps.mu.Lock()
	defer ps.mu.Unlock()

	for name, pres := range ps.items {
		if pres.CreatedAt.Before(cutoff) {
			os.RemoveAll(pres.Path)
			delete(ps.items, name)
		}
	}
}

// HandleUploadPresentation returns a handler that accepts a zip upload for a named presentation.
func HandleUploadPresentation(store *PresentationStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost && r.Method != http.MethodPut {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		name := r.PathValue("name")
		if name == "" {
			http.Error(w, "Missing presentation name", http.StatusBadRequest)
			return
		}

		// Limit upload to 100MB
		r.Body = http.MaxBytesReader(w, r.Body, 100<<20)

		// Accept multipart file upload with field name "file"
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			http.Error(w, fmt.Sprintf("Invalid upload: %v", err), http.StatusBadRequest)
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "Missing 'file' field in upload", http.StatusBadRequest)
			return
		}
		defer file.Close()

		// Verify it's a zip
		if !strings.HasSuffix(strings.ToLower(header.Filename), ".zip") &&
			header.Header.Get("Content-Type") != "application/zip" {
			http.Error(w, "Upload must be a zip file", http.StatusBadRequest)
			return
		}

		pres, err := store.Add(name, file)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to store presentation: %v", err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(PresentationInfo{
			Name:      pres.Name,
			CreatedAt: pres.CreatedAt.UTC().Format(time.RFC3339),
			Size:      pres.Size,
			SizeHuman: formatBytes(pres.Size),
		})
	}
}

// HandleListPresentations returns a handler that lists all uploaded presentations.
func HandleListPresentations(store *PresentationStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		presentations := store.List()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{
			"count":         len(presentations),
			"presentations": presentations,
		})
	}
}

// HandleDeletePresentation returns a handler that deletes a named presentation.
func HandleDeletePresentation(store *PresentationStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		name := r.PathValue("name")
		if name == "" {
			http.Error(w, "Missing presentation name", http.StatusBadRequest)
			return
		}

		if err := store.Remove(name); err != nil {
			http.Error(w, fmt.Sprintf("Failed to delete: %v", err), http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "deleted", "name": name})
	}
}

// HandleServePresentation returns a handler that serves files from an uploaded presentation.
func HandleServePresentation(presentationsDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if name == "" {
			http.Error(w, "Missing presentation name", http.StatusBadRequest)
			return
		}

		if !validPresentationName.MatchString(name) {
			http.Error(w, "Invalid presentation name", http.StatusBadRequest)
			return
		}

		presDir := filepath.Join(presentationsDir, name)
		if _, err := os.Stat(presDir); os.IsNotExist(err) {
			http.Error(w, "Presentation not found", http.StatusNotFound)
			return
		}

		// Strip the /p/{name} prefix and serve the remaining path
		prefix := "/p/" + name
		filePath := strings.TrimPrefix(r.URL.Path, prefix)
		if filePath == "" || filePath == "/" {
			filePath = "/index.html"
		}

		fullPath := filepath.Join(presDir, filePath)

		// Security: ensure the resolved path is within the presentation directory
		if !strings.HasPrefix(filepath.Clean(fullPath), filepath.Clean(presDir)+string(filepath.Separator)) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		http.ServeFile(w, r, fullPath)
	}
}

func formatBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
