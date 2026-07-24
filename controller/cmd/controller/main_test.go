package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/mayanklahiri/virtualme/controller/internal/health"
	"github.com/mayanklahiri/virtualme/controller/internal/tts"
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

func TestDesktopRootRedirectsToClient(t *testing.T) {
	desktopURL, _ := url.Parse("http://127.0.0.1:1")
	handler := newMux(redConfig(t), ws.NewHub(), desktopURL)
	for _, route := range []string{"/desktop", "/desktop/"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, route, nil))
		if response.Code != http.StatusFound {
			t.Fatalf("GET %s status = %d", route, response.Code)
		}
		want := "/desktop/vnc.html?autoconnect=1&resize=scale&path=desktop/websockify"
		if got := response.Header().Get("Location"); got != want {
			t.Fatalf("GET %s location = %q, want %q", route, got, want)
		}
	}
}

func fakeTTSServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		encoded := base64.StdEncoding.EncodeToString([]byte{1, 2, 3, 4})
		_, _ = w.Write([]byte(`{"type":"start","sampleRate":22050,"channels":1,"sentences":1}` + "\n"))
		_, _ = w.Write([]byte(`{"type":"chunk","seq":0,"pcm":"` + encoded + `"}` + "\n"))
		_, _ = w.Write([]byte(`{"type":"done","audioSec":1,"rtf":0.1}` + "\n"))
	}))
}

func TestSpeechEndpointWAVAndPCM(t *testing.T) {
	server := fakeTTSServer(t)
	defer server.Close()
	for _, format := range []string{"wav", "pcm"} {
		request := httptest.NewRequest(http.MethodPost, "/v1/audio/speech",
			strings.NewReader(`{"input":"Hello.","response_format":"`+format+`"}`))
		response := httptest.NewRecorder()
		speechHandler(&tts.Client{URL: server.URL}).ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d: %s", format, response.Code, response.Body.String())
		}
		body := response.Body.Bytes()
		if format == "wav" {
			if !bytes.HasPrefix(body, []byte("RIFF")) || !bytes.HasSuffix(body, []byte{1, 2, 3, 4}) {
				t.Fatalf("wav body = %x", body)
			}
		} else if !bytes.Equal(body, []byte{1, 2, 3, 4}) {
			t.Fatalf("pcm body = %x", body)
		}
	}
}

func TestSpeechEndpointErrors(t *testing.T) {
	bad := httptest.NewRecorder()
	speechHandler(&tts.Client{}).ServeHTTP(bad, httptest.NewRequest(http.MethodPost, "/v1/audio/speech",
		strings.NewReader(`{"input":"Hello.","response_format":"mp3"}`)))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("bad format status = %d", bad.Code)
	}
	down := httptest.NewRecorder()
	client := &tts.Client{URL: "http://127.0.0.1:1", HTTP: &http.Client{}}
	speechHandler(client).ServeHTTP(down, httptest.NewRequest(http.MethodPost, "/v1/audio/speech",
		strings.NewReader(`{"input":"Hello."}`)).WithContext(context.Background()))
	if down.Code != http.StatusBadGateway {
		t.Fatalf("down status = %d: %s", down.Code, down.Body.String())
	}
}

func TestSpeechEndpointMapsVoice(t *testing.T) {
	requestBody := make(chan tts.Request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var input tts.Request
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		requestBody <- input
		_, _ = w.Write([]byte(
			"{\"type\":\"start\",\"sampleRate\":22050,\"channels\":1,\"sentences\":1}\n" +
				"{\"type\":\"chunk\",\"seq\":0,\"pcm\":\"AQI=\"}\n" +
				"{\"type\":\"done\",\"audioSec\":1,\"rtf\":0.1}\n",
		))
	}))
	defer server.Close()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/audio/speech",
		strings.NewReader(`{"input":"Hello.","voice":"en_GB-alba-medium","response_format":"pcm"}`))
	speechHandler(&tts.Client{URL: server.URL}).ServeHTTP(response, request)
	if response.Header().Get("X-VM-Voice") != "en_GB-alba-medium" {
		t.Fatalf("voice header = %q", response.Header().Get("X-VM-Voice"))
	}
	if input := <-requestBody; input.Voice != "en_GB-alba-medium" {
		t.Fatalf("ttsd request voice = %q", input.Voice)
	}
}

func TestTTSWSReplacesAndStopsPerConnection(t *testing.T) {
	manager := newTTSWS(&tts.Client{})
	conn := new(ws.Conn)
	firstCtx, firstCancel, firstToken, oldID := manager.start(conn, "first")
	defer firstCancel()
	if oldID != "" {
		t.Fatalf("first old id = %q", oldID)
	}
	secondCtx, secondCancel, _, oldID := manager.start(conn, "second")
	defer secondCancel()
	if oldID != "first" {
		t.Fatalf("replacement old id = %q", oldID)
	}
	select {
	case <-firstCtx.Done():
	default:
		t.Fatal("replacement did not cancel first stream")
	}
	manager.done(conn, firstToken)
	if manager.stop(conn, "wrong") {
		t.Fatal("wrong id stopped active stream")
	}
	if !manager.stop(conn, "second") {
		t.Fatal("active stream was not stopped")
	}
	select {
	case <-secondCtx.Done():
	default:
		t.Fatal("stop did not cancel second stream")
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
	if len(report.Services) != 8 {
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
	for _, route := range []string{"/status", "/tools", "/desktop-view"} {
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

func TestManualToolPayloadValidationAndCap(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want bool
	}{
		{`{}`, true},
		{`{"topic":"os"}`, true},
		{`[]`, false},
		{`null`, false},
		{``, false},
	} {
		if got := jsonObject(json.RawMessage(test.raw)); got != test.want {
			t.Fatalf("jsonObject(%q) = %v, want %v", test.raw, got, test.want)
		}
	}
	text := capText(strings.Repeat("x", 20000), 16*1024)
	if len(text) != 16*1024 || !strings.HasSuffix(text, "…[truncated]") {
		t.Fatalf("capped text length/suffix = %d, %q", len(text), text[len(text)-20:])
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
		TTSHealthURL:   "http://127.0.0.1:1",
		Xdotool:        "/does/not/exist",
		SendmailPath:   "/does/not/exist",
		MailSpoolDir:   t.TempDir(),
	}
}
