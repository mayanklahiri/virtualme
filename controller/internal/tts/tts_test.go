package tts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func wavFixture(extra bool) []byte {
	var body bytes.Buffer
	body.WriteString("fmt ")
	_ = binary.Write(&body, binary.LittleEndian, uint32(16))
	_ = binary.Write(&body, binary.LittleEndian, uint16(1))
	_ = binary.Write(&body, binary.LittleEndian, uint16(1))
	_ = binary.Write(&body, binary.LittleEndian, uint32(22050))
	_ = binary.Write(&body, binary.LittleEndian, uint32(44100))
	_ = binary.Write(&body, binary.LittleEndian, uint16(2))
	_ = binary.Write(&body, binary.LittleEndian, uint16(16))
	if extra {
		body.WriteString("LIST")
		_ = binary.Write(&body, binary.LittleEndian, uint32(4))
		body.WriteString("test")
	}
	body.WriteString("data")
	_ = binary.Write(&body, binary.LittleEndian, uint32(8))
	body.Write([]byte{1, 2, 3, 4, 5, 6, 7, 8})
	var file bytes.Buffer
	file.WriteString("RIFF")
	_ = binary.Write(&file, binary.LittleEndian, uint32(body.Len()+4))
	file.WriteString("WAVE")
	file.Write(body.Bytes())
	return file.Bytes()
}

func TestSplitSentences(t *testing.T) {
	if got := SplitSentences("This is the first sentence. This is another sentence! This is the final sentence?"); len(got) != 3 {
		t.Fatalf("basic split = %#v", got)
	}
	got := SplitSentences("Hi. This fragment must be merged into the following sentence.")
	if len(got) != 1 || !strings.HasPrefix(got[0], "Hi. This") {
		t.Fatalf("short merge = %#v", got)
	}
	long := strings.Repeat("word ", 80)
	got = SplitSentences(long)
	if len(got) < 2 {
		t.Fatalf("hard split = %#v", got)
	}
	for _, part := range got {
		if len([]rune(part)) > 300 {
			t.Fatalf("part exceeds 300 runes: %d", len([]rune(part)))
		}
	}
}

func TestReadWAVWalksChunks(t *testing.T) {
	for _, extra := range []bool{false, true} {
		name := filepath.Join(t.TempDir(), "fixture.wav")
		if err := os.WriteFile(name, wavFixture(extra), 0o600); err != nil {
			t.Fatal(err)
		}
		pcm, rate, channels, err := ReadWAV(name)
		if err != nil || rate != 22050 || channels != 1 || len(pcm) != 8 {
			t.Fatalf("ReadWAV(extra=%v) = %d, %d, %d, %v", extra, len(pcm), rate, channels, err)
		}
	}
}

func TestHealthRequiresVoiceFiles(t *testing.T) {
	modelDir := t.TempDir()
	service := NewService(Config{ModelDir: modelDir})
	response := httptest.NewRecorder()
	service.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing model health = %d", response.Code)
	}
	voiceDir := filepath.Join(modelDir, "vits-piper-"+DefaultVoice)
	if err := os.Mkdir(voiceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{DefaultVoice + ".onnx", "tokens.txt"} {
		if err := os.WriteFile(filepath.Join(voiceDir, name), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(voiceDir, "espeak-ng-data"), 0o700); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	service.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"voice":"`+DefaultVoice+`"`) {
		t.Fatalf("healthy response = %d: %s", response.Code, response.Body.String())
	}
}

type fixtureRunner struct {
	mu      sync.Mutex
	args    [][]string
	block   bool
	stopped chan struct{}
}

func (f *fixtureRunner) Run(ctx context.Context, _ string, args, _ []string) error {
	f.mu.Lock()
	f.args = append(f.args, append([]string(nil), args...))
	f.mu.Unlock()
	if f.block {
		<-ctx.Done()
		close(f.stopped)
		return ctx.Err()
	}
	for _, arg := range args {
		if name, ok := strings.CutPrefix(arg, "--output-filename="); ok {
			return os.WriteFile(name, wavFixture(false), 0o600)
		}
	}
	return nil
}

func TestSynthesizeHandlerStreamAndArguments(t *testing.T) {
	runner := &fixtureRunner{}
	service := NewService(Config{SherpaDir: "/sherpa", ModelDir: "/voice", MaxChars: 20, Runner: runner})
	request := httptest.NewRequest(http.MethodPost, "/synthesize", strings.NewReader(`{"text":"One sentence. Two.","speed":9}`))
	response := httptest.NewRecorder()
	service.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var types []string
	scanner := json.NewDecoder(response.Body)
	for scanner.More() {
		var event Event
		if err := scanner.Decode(&event); err != nil {
			t.Fatal(err)
		}
		types = append(types, event.Type)
		if event.Type == "chunk" {
			if _, err := base64.StdEncoding.DecodeString(event.PCM); err != nil {
				t.Fatal(err)
			}
		}
	}
	if strings.Join(types, ",") != "start,chunk,done" {
		t.Fatalf("events = %v", types)
	}
	runner.mu.Lock()
	argv := strings.Join(runner.args[0], " ")
	runner.mu.Unlock()
	for _, want := range []string{
		"--vits-model=/voice/vits-piper-" + DefaultVoice + "/" + DefaultVoice + ".onnx",
		"--vits-tokens=/voice/vits-piper-" + DefaultVoice + "/tokens.txt",
		"--vits-data-dir=/voice/vits-piper-" + DefaultVoice + "/espeak-ng-data",
		"--vits-length-scale=0.5",
	} {
		if !strings.Contains(argv, want) {
			t.Errorf("argv %q missing %q", argv, want)
		}
	}

	tooLong := httptest.NewRecorder()
	service.Handler().ServeHTTP(tooLong, httptest.NewRequest(http.MethodPost, "/synthesize", strings.NewReader(`{"text":"123456789012345678901"}`)))
	if tooLong.Code != http.StatusBadRequest {
		t.Fatalf("char cap status = %d", tooLong.Code)
	}

	unsplit := httptest.NewRecorder()
	service.Handler().ServeHTTP(unsplit, httptest.NewRequest(http.MethodPost, "/synthesize",
		strings.NewReader(`{"text":"First. Second.","split":false}`)))
	if strings.Count(unsplit.Body.String(), `"type":"chunk"`) != 1 {
		t.Fatalf("split:false stream = %s", unsplit.Body.String())
	}
}

func TestSynthesizeCancellationReachesRunner(t *testing.T) {
	runner := &fixtureRunner{block: true, stopped: make(chan struct{})}
	service := NewService(Config{SherpaDir: "/s", ModelDir: "/m", Runner: runner})
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodPost, "/synthesize", strings.NewReader(`{"text":"cancel this request"}`)).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		service.Handler().ServeHTTP(httptest.NewRecorder(), request)
		close(done)
	}()
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case <-runner.stopped:
	case <-time.After(time.Second):
		t.Fatal("runner context was not canceled")
	}
	<-done
}

func TestClientStreamsEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write([]byte("{\"type\":\"start\",\"sampleRate\":16000,\"channels\":1,\"sentences\":1}\n"))
		_, _ = w.Write([]byte("{\"type\":\"chunk\",\"seq\":0,\"pcm\":\"AQI=\"}\n"))
		_, _ = w.Write([]byte("{\"type\":\"done\",\"audioSec\":1.5,\"rtf\":0.2,\"cached\":true}\n"))
	}))
	defer server.Close()
	var events []string
	store := new(memoryList)
	client := &Client{URL: server.URL, Log: NewLog(store, nil)}
	summary, err := client.Synthesize(context.Background(), Request{
		Text: "hello", Voice: "en_GB-alba-medium", Origin: "api",
	}, func(event Event) error {
		events = append(events, event.Type)
		return nil
	})
	if err != nil || strings.Join(events, ",") != "start,chunk,done" || summary.AudioSec != 1.5 || !summary.Cached {
		t.Fatalf("Synthesize = %+v, %v, %v", summary, events, err)
	}
	if len(store.values) != 1 || !strings.Contains(store.values[0], `"origin":"api"`) ||
		!strings.Contains(store.values[0], `"voice":"en_GB-alba-medium"`) ||
		!strings.Contains(store.values[0], `"cached":true`) {
		t.Fatalf("speech log = %v", store.values)
	}
}
