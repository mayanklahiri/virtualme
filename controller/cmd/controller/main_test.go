package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/mayanklahiri/virtualme/controller/internal/health"
	"github.com/mayanklahiri/virtualme/controller/internal/ws"
)

func TestDesktopProxyStripsPrefix(t *testing.T) {
	path := make(chan string, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		path <- request.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()
	desktopURL, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/desktop/vnc.html", nil)
	response := httptest.NewRecorder()
	newMux(redConfig(t), ws.NewHub(), desktopURL).ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
	if got := <-path; got != "/vnc.html" {
		t.Fatalf("backend path = %q", got)
	}
}

func TestHealthRouteReturnsServices(t *testing.T) {
	desktopURL, _ := url.Parse("http://127.0.0.1:1")
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()
	newMux(redConfig(t), ws.NewHub(), desktopURL).ServeHTTP(response, request)
	var report struct {
		Services []health.Service `json:"services"`
	}
	if err := json.NewDecoder(response.Body).Decode(&report); err != nil {
		t.Fatal(err)
	}
	if len(report.Services) != 6 {
		t.Fatalf("services = %d", len(report.Services))
	}
}

func TestRootServesEmbeddedSPA(t *testing.T) {
	desktopURL, _ := url.Parse("http://127.0.0.1:1")
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	newMux(redConfig(t), ws.NewHub(), desktopURL).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), "Virtual Me") {
		t.Fatal("root did not serve the SPA")
	}
}

func TestSPAFallbackAndMissingAsset(t *testing.T) {
	desktopURL, _ := url.Parse("http://127.0.0.1:1")
	handler := newMux(redConfig(t), ws.NewHub(), desktopURL)
	for _, route := range []string{"/status", "/desktop-view"} {
		request := httptest.NewRequest(http.MethodGet, route, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Virtual Me") {
			t.Fatalf("GET %s = %d, body %q", route, response.Code, response.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/js/nope.js", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing asset status = %d, want 404", response.Code)
	}
}

func redConfig(t *testing.T) health.Config {
	t.Helper()
	return health.Config{
		Display:        ":99",
		X11SocketDir:   t.TempDir(),
		VNCAddr:        "127.0.0.1:1",
		NoVNCURL:       "http://127.0.0.1:1",
		ValkeyAddr:     "127.0.0.1:1",
		LlamaHealthURL: "http://127.0.0.1:1",
		Xdotool:        "/does/not/exist",
	}
}
