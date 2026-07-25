package datafs

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListAndFileContainment(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "metrics"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "metrics", "a.json"), []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "readme.txt"), []byte(strings.Repeat("x", textCap+10)), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "metrics"), filepath.Join(root, "metrics-link")); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	Mount(mux, root)

	t.Run("list root", func(t *testing.T) {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/data/list", nil))
		if response.Code != 200 {
			t.Fatalf("status = %d", response.Code)
		}
		var body struct {
			Entries []entry `json:"entries"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		names := make([]string, 0, len(body.Entries))
		for _, item := range body.Entries {
			names = append(names, item.Name+":"+item.Kind)
		}
		joined := strings.Join(names, ",")
		if !strings.Contains(joined, "metrics:dir") || !strings.Contains(joined, "readme.txt:file") {
			t.Fatalf("entries = %v", names)
		}
		if strings.Contains(joined, "escape") {
			t.Fatalf("escaping symlink was listed: %v", names)
		}
		if !strings.Contains(joined, "metrics-link:dir") {
			t.Fatalf("internal symlink missing: %v", names)
		}
		// dirs first
		if body.Entries[0].Kind != "dir" {
			t.Fatalf("first entry kind = %s", body.Entries[0].Kind)
		}
	})

	t.Run("traversal rejected", func(t *testing.T) {
		for _, path := range []string{
			"/api/data/file?path=../../etc/passwd",
			"/api/data/file?path=%2e%2e%2f%2e%2e%2fetc%2fpasswd",
			"/api/data/file?path=/etc/passwd",
			"/api/data/file?path=escape",
		} {
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			if response.Code != 404 {
				t.Fatalf("%s => %d", path, response.Code)
			}
		}
	})

	t.Run("text cap", func(t *testing.T) {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/data/file?path=readme.txt", nil))
		if response.Code != 200 {
			t.Fatalf("status = %d", response.Code)
		}
		if response.Header().Get("X-VM-Truncated") != "1" {
			t.Fatal("expected truncation header")
		}
		if response.Body.Len() != textCap {
			t.Fatalf("body len = %d", response.Body.Len())
		}
	})

	t.Run("download uncapped", func(t *testing.T) {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/data/file?path=readme.txt&download=1", nil))
		if response.Code != 200 {
			t.Fatalf("status = %d", response.Code)
		}
		if !strings.Contains(response.Header().Get("Content-Disposition"), "attachment") {
			t.Fatalf("disposition = %q", response.Header().Get("Content-Disposition"))
		}
		if response.Body.Len() != textCap+10 {
			t.Fatalf("body len = %d", response.Body.Len())
		}
	})

	t.Run("method and type mismatches", func(t *testing.T) {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/data/list", nil))
		if response.Code != 405 {
			t.Fatalf("POST list = %d", response.Code)
		}
		response = httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/data/file?path=metrics", nil))
		if response.Code != 400 {
			t.Fatalf("file on dir = %d", response.Code)
		}
		response = httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/data/list?path=readme.txt", nil))
		if response.Code != 400 {
			t.Fatalf("list on file = %d", response.Code)
		}
	})
}
