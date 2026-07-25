package datafs

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type testCache struct {
	mu      sync.Mutex
	values  map[string]string
	setTTL  int
	setHits int
}

func (c *testCache) Get(key string) (*string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.values[key]
	if !ok {
		return nil, nil
	}
	return &value, nil
}

func (c *testCache) SetEx(key, value string, seconds int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.values == nil {
		c.values = make(map[string]string)
	}
	c.values[key] = value
	c.setTTL = seconds
	c.setHits++
	return nil
}

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
	Mount(mux, root, nil)

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
			"/api/data/du?path=../../etc",
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
		response = httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/data/du?path=readme.txt", nil))
		if response.Code != 400 {
			t.Fatalf("du on file = %d", response.Code)
		}
		response = httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/data/du", nil))
		if response.Code != 405 {
			t.Fatalf("POST du = %d", response.Code)
		}
	})
}

func TestDURecursiveSizesAndCache(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "one", "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "one", "a"), []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "b"), []byte("1234"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "one", "a"), filepath.Join(nested, "link")); err != nil {
		t.Fatal(err)
	}
	cache := &testCache{values: make(map[string]string)}
	mux := http.NewServeMux()
	Mount(mux, root, cache)

	readSize := func() int64 {
		t.Helper()
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/data/du", nil))
		if response.Code != 200 {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
		var body struct {
			Sizes map[string]int64 `json:"sizes"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Sizes["one"]
	}

	if got := readSize(); got != 7 {
		t.Fatalf("size = %d, want 7", got)
	}
	if cache.setHits != 1 || cache.setTTL != duTTL {
		t.Fatalf("cache writes = %d, ttl = %d", cache.setHits, cache.setTTL)
	}
	if err := os.WriteFile(filepath.Join(root, "one", "a"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readSize(); got != 7 {
		t.Fatalf("cached size = %d, want 7", got)
	}
	if cache.setHits != 1 {
		t.Fatalf("cache writes after hit = %d", cache.setHits)
	}
}

func TestDirectorySizeCoalescesConcurrentWalks(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	joined := make(chan struct{}, 1)
	var calls int
	fs := &FS{
		Root: t.TempDir(),
		walk: func(string) (int64, error) {
			calls++
			close(started)
			<-release
			return 42, nil
		},
		onDUWait: func() { joined <- struct{}{} },
	}
	results := make(chan int64, 2)
	go func() {
		size, _ := fs.directorySize("one", filepath.Join(fs.Root, "one"))
		results <- size
	}()
	<-started
	go func() {
		size, _ := fs.directorySize("one", filepath.Join(fs.Root, "one"))
		results <- size
	}()
	<-joined
	close(release)
	for range 2 {
		if got := <-results; got != 42 {
			t.Fatalf("size = %d, want 42", got)
		}
	}
	if calls != 1 {
		t.Fatalf("walk calls = %d, want 1", calls)
	}
}
