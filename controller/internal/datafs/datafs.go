// Package datafs serves a read-only HTTP view of $VM_DATA_DIR.
package datafs

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const textCap = 262144

// FS is a read-only explorer rooted at Root.
type FS struct {
	Root string
}

type entry struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Size    int64  `json:"size"`
	MtimeMs int64  `json:"mtimeMs"`
}

func (f *FS) resolveRoot() (string, error) {
	return filepath.EvalSymlinks(f.Root)
}

func (f *FS) resolve(rel string) (string, error) {
	if rel == "" || rel == "." {
		return f.resolveRoot()
	}
	if filepath.IsAbs(rel) || strings.Contains(rel, "..") {
		return "", os.ErrNotExist
	}
	root, err := f.resolveRoot()
	if err != nil {
		return "", err
	}
	candidate := filepath.Clean(filepath.Join(root, rel))
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		// Dangling symlink or missing path.
		if _, statErr := os.Lstat(candidate); statErr != nil {
			return "", os.ErrNotExist
		}
		return "", os.ErrNotExist
	}
	prefix := root + string(os.PathSeparator)
	if resolved != root && !strings.HasPrefix(resolved, prefix) {
		return "", os.ErrNotExist
	}
	return resolved, nil
}

// List handles GET /api/data/list?path=
func (f *FS) List(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rel := r.URL.Query().Get("path")
	target, err := f.resolve(rel)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	info, err := os.Stat(target)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !info.IsDir() {
		http.Error(w, "not a directory", http.StatusBadRequest)
		return
	}
	dirents, err := os.ReadDir(target)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	entries := make([]entry, 0, len(dirents))
	root, _ := f.resolveRoot()
	for _, dirent := range dirents {
		name := dirent.Name()
		child := filepath.Join(target, name)
		if dirent.Type()&os.ModeSymlink != 0 {
			resolved, resolveErr := filepath.EvalSymlinks(child)
			if resolveErr != nil {
				continue
			}
			prefix := root + string(os.PathSeparator)
			if resolved != root && !strings.HasPrefix(resolved, prefix) {
				continue
			}
			info, infoErr := os.Stat(resolved)
			if infoErr != nil {
				continue
			}
			kind := "file"
			size := info.Size()
			if info.IsDir() {
				kind = "dir"
				size = 0
			}
			entries = append(entries, entry{
				Name: name, Kind: kind, Size: size, MtimeMs: info.ModTime().UnixMilli(),
			})
			continue
		}
		info, infoErr := dirent.Info()
		if infoErr != nil {
			continue
		}
		kind := "file"
		size := info.Size()
		if info.IsDir() {
			kind = "dir"
			size = 0
		}
		entries = append(entries, entry{
			Name: name, Kind: kind, Size: size, MtimeMs: info.ModTime().UnixMilli(),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Kind != entries[j].Kind {
			return entries[i].Kind == "dir"
		}
		return entries[i].Name < entries[j].Name
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"path": rel, "entries": entries})
}

// File handles GET /api/data/file?path=&download=
func (f *FS) File(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rel := r.URL.Query().Get("path")
	if rel == "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	target, err := f.resolve(rel)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	info, err := os.Stat(target)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if info.IsDir() {
		http.Error(w, "not a file", http.StatusBadRequest)
		return
	}
	file, err := os.Open(target)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer file.Close()

	contentType := contentTypeFor(target, file)
	download := r.URL.Query().Get("download") == "1"
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Last-Modified", info.ModTime().UTC().Format(http.TimeFormat))
	w.Header().Set("X-VM-Size", strconv.FormatInt(info.Size(), 10))
	if download {
		w.Header().Set("Content-Disposition", "attachment; filename=\""+filepath.Base(target)+"\"")
		http.ServeContent(w, r, filepath.Base(target), info.ModTime(), file)
		return
	}
	if isTextual(contentType) && info.Size() > textCap {
		limited := io.LimitReader(file, textCap)
		w.Header().Set("X-VM-Truncated", "1")
		_, _ = io.Copy(w, limited)
		return
	}
	http.ServeContent(w, r, filepath.Base(target), info.ModTime(), file)
}

func contentTypeFor(path string, file *os.File) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".json":
		return "application/json"
	case ".jsonl":
		return "application/x-ndjson"
	case ".yaml", ".yml":
		return "text/yaml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".wav":
		return "audio/wav"
	case ".txt", ".log", ".md", ".sh", ".go", ".js", ".css", ".html", ".pem":
		return "text/plain; charset=utf-8"
	}
	buf := make([]byte, 512)
	n, err := file.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return "application/octet-stream"
	}
	_, _ = file.Seek(0, io.SeekStart)
	detected := http.DetectContentType(buf[:n])
	if detected == "" {
		return "application/octet-stream"
	}
	return detected
}

func isTextual(contentType string) bool {
	return strings.HasPrefix(contentType, "text/") ||
		contentType == "application/json" ||
		contentType == "application/x-ndjson" ||
		contentType == "text/yaml"
}

// Mount registers the list/file handlers on mux.
func Mount(mux *http.ServeMux, root string) {
	fs := &FS{Root: root}
	mux.HandleFunc("/api/data/list", fs.List)
	mux.HandleFunc("/api/data/file", fs.File)
}
