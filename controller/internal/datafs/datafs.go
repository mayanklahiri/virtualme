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
	"sync"
)

const (
	textCap   = 262144
	duTTL     = 300
	duKeyBase = "vm:datafs:du:"
)

// Cache is the subset of Valkey used for directory-size summaries.
type Cache interface {
	Get(key string) (*string, error)
	SetEx(key, value string, seconds int) error
}

// FS is a read-only explorer rooted at Root.
type FS struct {
	Root  string
	Cache Cache

	duMu      sync.Mutex
	duPending map[string]*duCall
	walk      func(string) (int64, error)
	onDUWait  func()
}

type duCall struct {
	done chan struct{}
	size int64
	err  error
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

// DU handles GET /api/data/du?path= and returns recursive sizes for child dirs.
func (f *FS) DU(w http.ResponseWriter, r *http.Request) {
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
	sizes := make(map[string]int64)
	for _, dirent := range dirents {
		childRel := dirent.Name()
		if rel != "" && rel != "." {
			childRel = filepath.Join(rel, dirent.Name())
		}
		if dirent.Type()&os.ModeSymlink != 0 {
			resolved, resolveErr := f.resolve(childRel)
			if resolveErr != nil {
				continue
			}
			resolvedInfo, statErr := os.Stat(resolved)
			if statErr == nil && resolvedInfo.IsDir() {
				sizes[dirent.Name()] = 0
			}
			continue
		}
		if !dirent.IsDir() {
			continue
		}
		size, sizeErr := f.directorySize(childRel, filepath.Join(target, dirent.Name()))
		if sizeErr != nil {
			continue
		}
		sizes[dirent.Name()] = size
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"path": rel, "sizes": sizes})
}

func (f *FS) directorySize(rel, target string) (int64, error) {
	key := duKeyBase + filepath.ToSlash(rel)
	if f.Cache != nil {
		if cached, err := f.Cache.Get(key); err == nil && cached != nil {
			if size, parseErr := strconv.ParseInt(*cached, 10, 64); parseErr == nil {
				return size, nil
			}
		}
	}

	f.duMu.Lock()
	if f.duPending == nil {
		f.duPending = make(map[string]*duCall)
	}
	if pending := f.duPending[key]; pending != nil {
		if f.onDUWait != nil {
			f.onDUWait()
		}
		f.duMu.Unlock()
		<-pending.done
		return pending.size, pending.err
	}
	call := &duCall{done: make(chan struct{})}
	f.duPending[key] = call
	f.duMu.Unlock()

	walk := f.walk
	if walk == nil {
		walk = walkSize
	}
	call.size, call.err = walk(target)
	if call.err == nil && f.Cache != nil {
		_ = f.Cache.SetEx(key, strconv.FormatInt(call.size, 10), duTTL)
	}

	f.duMu.Lock()
	delete(f.duPending, key)
	close(call.done)
	f.duMu.Unlock()
	return call.size, call.err
}

func walkSize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, dirent os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != root && dirent.Type()&os.ModeSymlink != 0 {
			if dirent.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if dirent.IsDir() {
			return nil
		}
		info, infoErr := dirent.Info()
		if infoErr != nil {
			return infoErr
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total, err
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
func Mount(mux *http.ServeMux, root string, cache Cache) {
	fs := &FS{Root: root, Cache: cache}
	mux.HandleFunc("/api/data/list", fs.List)
	mux.HandleFunc("/api/data/file", fs.File)
	mux.HandleFunc("/api/data/du", fs.DU)
}
