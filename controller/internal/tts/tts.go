// Package tts implements local sentence-level speech synthesis and its
// streaming controller client.
package tts

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// Voices is the complete set of image-baked Piper voices. The first is the
// default used for omitted or unknown voice names.
var Voices = []string{"en_US-lessac-medium", "en_GB-alba-medium"}

const DefaultVoice = "en_US-lessac-medium"

// NormalizeVoice preserves the permissive API behavior while constraining
// model paths to image-baked voices.
func NormalizeVoice(voice string) string {
	for _, available := range Voices {
		if voice == available {
			return voice
		}
	}
	if voice != "" {
		log.Printf("tts: unknown voice %q; using %s", voice, DefaultVoice)
	}
	return DefaultVoice
}

// Request is accepted by ttsd's synthesis endpoint.
type Request struct {
	Text   string  `json:"text"`
	Speed  float64 `json:"speed,omitempty"`
	Split  *bool   `json:"split,omitempty"`
	Voice  string  `json:"voice,omitempty"`
	Origin string  `json:"-"`
}

// Event is one NDJSON synthesis event.
type Event struct {
	Type       string  `json:"type"`
	SampleRate int     `json:"sampleRate,omitempty"`
	Channels   int     `json:"channels,omitempty"`
	Sentences  int     `json:"sentences,omitempty"`
	Seq        int     `json:"seq"`
	Sentence   string  `json:"sentence,omitempty"`
	PCM        string  `json:"pcm,omitempty"`
	GenMS      int64   `json:"genMs,omitempty"`
	AudioSec   float64 `json:"audioSec,omitempty"`
	RTF        float64 `json:"rtf,omitempty"`
	Cached     bool    `json:"cached,omitempty"`
	Error      string  `json:"error,omitempty"`
}

// Summary is returned after a complete synthesis stream.
type Summary struct {
	SampleRate int
	Channels   int
	Sentences  int
	AudioSec   float64
	RTF        float64
	Cached     bool
}

// Runner invokes the pinned offline TTS executable.
type Runner interface {
	Run(context.Context, string, []string, []string) error
}

type processRunner struct{}

func (processRunner) Run(ctx context.Context, name string, args, env []string) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Env = os.Environ()
	for _, override := range env {
		key, _, ok := strings.Cut(override, "=")
		if !ok {
			continue
		}
		filtered := command.Env[:0]
		for _, entry := range command.Env {
			if !strings.HasPrefix(entry, key+"=") {
				filtered = append(filtered, entry)
			}
		}
		command.Env = append(filtered, override)
	}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		<-done
		return ctx.Err()
	}
}

// Config configures the local synthesis service.
type Config struct {
	SherpaDir     string
	ModelDir      string
	CacheDir      string
	CacheMaxBytes int64
	MaxChars      int
	Runner        Runner
}

// Service serializes CPU-bound synthesis requests.
type Service struct {
	cfg   Config
	queue chan struct{}
}

// NewService creates a synthesis service.
func NewService(cfg Config) *Service {
	if cfg.MaxChars <= 0 {
		cfg.MaxChars = 4096
	}
	if cfg.Runner == nil {
		cfg.Runner = processRunner{}
	}
	queue := make(chan struct{}, 1)
	queue <- struct{}{}
	return &Service{cfg: cfg, queue: queue}
}

func (s *Service) modelFiles(voice string) (string, string, string) {
	dir := filepath.Join(s.cfg.ModelDir, "vits-piper-"+voice)
	return filepath.Join(dir, voice+".onnx"),
		filepath.Join(dir, "tokens.txt"),
		filepath.Join(dir, "espeak-ng-data")
}

func (s *Service) healthy() bool {
	model, tokens, data := s.modelFiles(DefaultVoice)
	for _, name := range []string{model, tokens} {
		if info, err := os.Stat(name); err != nil || !info.Mode().IsRegular() {
			return false
		}
	}
	info, err := os.Stat(data)
	return err == nil && info.IsDir()
}

// AvailableVoices returns image-baked voice directories found at startup.
func (s *Service) AvailableVoices() []string {
	available := make([]string, 0, len(Voices))
	for _, voice := range Voices {
		model, tokens, data := s.modelFiles(voice)
		modelInfo, modelErr := os.Stat(model)
		tokenInfo, tokenErr := os.Stat(tokens)
		dataInfo, dataErr := os.Stat(data)
		if modelErr == nil && modelInfo.Mode().IsRegular() &&
			tokenErr == nil && tokenInfo.Mode().IsRegular() &&
			dataErr == nil && dataInfo.IsDir() {
			available = append(available, voice)
		}
	}
	return available
}

// Handler serves ttsd's health and synthesis routes.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/synthesize", s.handleSynthesize)
	return mux
}

func (s *Service) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	status := http.StatusOK
	if !s.healthy() {
		status = http.StatusServiceUnavailable
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": status == http.StatusOK, "voice": DefaultVoice})
}

func parseRequest(r *http.Request, maxChars int) (Request, []string, error) {
	var request Request
	decoder := json.NewDecoder(io.LimitReader(r.Body, int64(maxChars*4+1024)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return request, nil, fmt.Errorf("invalid JSON: %w", err)
	}
	request.Text = strings.TrimSpace(request.Text)
	if request.Text == "" {
		return request, nil, errors.New("text is required")
	}
	if utf8.RuneCountInString(request.Text) > maxChars {
		return request, nil, fmt.Errorf("text exceeds %d characters", maxChars)
	}
	if request.Speed == 0 {
		request.Speed = 1
	}
	request.Speed = max(0.5, min(2, request.Speed))
	request.Voice = NormalizeVoice(request.Voice)
	split := request.Split == nil || *request.Split
	sentences := []string{request.Text}
	if split {
		sentences = SplitSentences(request.Text)
	}
	return request, sentences, nil
}

func (s *Service) handleSynthesize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	request, sentences, err := parseRequest(r, s.cfg.MaxChars)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	select {
	case <-r.Context().Done():
		return
	case <-s.queue:
	}
	defer func() { s.queue <- struct{}{} }()

	w.Header().Set("Content-Type", "application/x-ndjson")
	flusher, _ := w.(http.Flusher)
	encoder := json.NewEncoder(w)
	emit := func(event Event) bool {
		if err := encoder.Encode(event); err != nil {
			return false
		}
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}
	started := time.Now()
	totalSamples := 0
	sampleRate := 0
	allCached := true
	for index, sentence := range sentences {
		generated := time.Now()
		pcm, rate, channelCount, cached, synthErr := s.synthesize(r.Context(), sentence, request.Voice, request.Speed)
		if synthErr != nil {
			if r.Context().Err() == nil {
				emit(Event{Type: "error", Error: synthErr.Error()})
			}
			return
		}
		allCached = allCached && cached
		if index == 0 {
			sampleRate = rate
			if !emit(Event{Type: "start", SampleRate: rate, Channels: channelCount, Sentences: len(sentences)}) {
				return
			}
		}
		totalSamples += len(pcm) / (2 * channelCount)
		if !emit(Event{
			Type: "chunk", Seq: index, Sentence: sentence,
			PCM: base64.StdEncoding.EncodeToString(pcm), GenMS: time.Since(generated).Milliseconds(),
		}) {
			return
		}
	}
	audioSec := float64(totalSamples) / float64(sampleRate)
	rtf := 0.0
	if audioSec > 0 {
		rtf = time.Since(started).Seconds() / audioSec
	}
	emit(Event{Type: "done", AudioSec: audioSec, RTF: rtf, Cached: allCached})
}

func (s *Service) synthesize(ctx context.Context, sentence, voice string, speed float64) ([]byte, int, int, bool, error) {
	if s.cfg.CacheDir != "" {
		if pcm, rate, channels, ok := s.readCache(voice, speed, sentence); ok {
			return pcm, rate, channels, true, nil
		}
	}
	file, err := os.CreateTemp("", "virtualme-tts-*.wav")
	if err != nil {
		return nil, 0, 0, false, err
	}
	name := file.Name()
	_ = file.Close()
	defer os.Remove(name)
	model, tokens, data := s.modelFiles(voice)
	args := []string{
		"--vits-model=" + model,
		"--vits-tokens=" + tokens,
		"--vits-data-dir=" + data,
		"--vits-length-scale=" + strconv.FormatFloat(1/speed, 'f', -1, 64),
		"--output-filename=" + name,
		sentence,
	}
	binaryPath := filepath.Join(s.cfg.SherpaDir, "bin", "sherpa-onnx-offline-tts")
	if err := s.cfg.Runner.Run(ctx, binaryPath, args, []string{"LD_LIBRARY_PATH=" + filepath.Join(s.cfg.SherpaDir, "lib")}); err != nil {
		return nil, 0, 0, false, fmt.Errorf("sherpa synthesis: %w", err)
	}
	pcm, rate, channels, err := ReadWAV(name)
	if err != nil {
		return nil, 0, 0, false, err
	}
	if s.cfg.CacheDir != "" {
		if err := s.writeCache(voice, speed, sentence, name); err != nil {
			log.Printf("tts: cache write: %v", err)
		}
	}
	return pcm, rate, channels, false, nil
}

// ReadWAV extracts s16le mono PCM by walking RIFF chunks.
func ReadWAV(name string) ([]byte, int, int, error) {
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, 0, 0, err
	}
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, 0, 0, errors.New("invalid RIFF/WAVE file")
	}
	rate, channels, bits := 0, 0, 0
	var pcm []byte
	for offset := 12; offset+8 <= len(data); {
		size := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		start, end := offset+8, offset+8+size
		if end > len(data) {
			return nil, 0, 0, errors.New("truncated WAV chunk")
		}
		switch string(data[offset : offset+4]) {
		case "fmt ":
			if size < 16 || binary.LittleEndian.Uint16(data[start:start+2]) != 1 {
				return nil, 0, 0, errors.New("WAV must contain PCM")
			}
			channels = int(binary.LittleEndian.Uint16(data[start+2 : start+4]))
			rate = int(binary.LittleEndian.Uint32(data[start+4 : start+8]))
			bits = int(binary.LittleEndian.Uint16(data[start+14 : start+16]))
		case "data":
			pcm = append([]byte(nil), data[start:end]...)
		}
		offset = end + size%2
	}
	if rate <= 0 || channels != 1 || bits != 16 || pcm == nil {
		return nil, 0, 0, errors.New("WAV must be s16le mono")
	}
	return pcm, rate, channels, nil
}

// SplitSentences applies punctuation, blank-line, short-fragment, and hard
// length boundaries without language-specific abbreviation heuristics.
func SplitSentences(text string) []string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	var raw []string
	start := 0
	runes := []rune(text)
	for index, r := range runes {
		boundary := r == '.' || r == '!' || r == '?' || r == '…'
		if boundary && (index+1 == len(runes) || isSpace(runes[index+1])) {
			raw = append(raw, strings.TrimSpace(string(runes[start:index+1])))
			start = index + 1
			continue
		}
		if r == '\n' && index+1 < len(runes) && runes[index+1] == '\n' {
			raw = append(raw, strings.TrimSpace(string(runes[start:index])))
			start = index + 2
		}
	}
	if tail := strings.TrimSpace(string(runes[start:])); tail != "" {
		raw = append(raw, tail)
	}
	clean := raw[:0]
	for index := 0; index < len(raw); index++ {
		part := strings.TrimSpace(raw[index])
		if part == "" {
			continue
		}
		if utf8.RuneCountInString(part) < 20 && index+1 < len(raw) {
			raw[index+1] = part + " " + strings.TrimSpace(raw[index+1])
			continue
		}
		clean = append(clean, part)
	}
	var result []string
	for _, sentence := range clean {
		for len([]rune(sentence)) > 300 {
			part := []rune(sentence)
			cut := 300
			for cut > 0 && !isSpace(part[cut]) {
				cut--
			}
			if cut == 0 {
				cut = 300
			}
			result = append(result, strings.TrimSpace(string(part[:cut])))
			sentence = strings.TrimSpace(string(part[cut:]))
		}
		if sentence != "" {
			result = append(result, sentence)
		}
	}
	return result
}

func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}

// Client streams events from ttsd.
type Client struct {
	URL  string
	HTTP *http.Client
	Log  *Log
}

// Synthesize posts a request and invokes onEvent for every stream event.
func (c *Client) Synthesize(ctx context.Context, request Request, onEvent func(Event) error) (Summary, error) {
	request.Voice = NormalizeVoice(request.Voice)
	if request.Speed == 0 {
		request.Speed = 1
	}
	started := time.Now()
	body, _ := json.Marshal(request)
	target := strings.TrimSuffix(c.URL, "/") + "/synthesize"
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return Summary{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	client := c.HTTP
	if client == nil {
		client = &http.Client{}
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return Summary{}, fmt.Errorf("ttsd unavailable: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return Summary{}, fmt.Errorf("ttsd returned %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	var summary Summary
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return summary, fmt.Errorf("invalid ttsd stream: %w", err)
		}
		switch event.Type {
		case "start":
			summary.SampleRate, summary.Channels, summary.Sentences = event.SampleRate, event.Channels, event.Sentences
		case "done":
			summary.AudioSec, summary.RTF, summary.Cached = event.AudioSec, event.RTF, event.Cached
		case "error":
			return summary, errors.New(event.Error)
		}
		if onEvent != nil {
			if err := onEvent(event); err != nil {
				return summary, err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return summary, err
	}
	if summary.SampleRate == 0 {
		return summary, errors.New("ttsd stream ended before start")
	}
	if c.Log != nil && request.Origin != "" {
		if err := c.Log.Record(Entry{
			Timestamp: time.Now().UnixMilli(), Origin: request.Origin,
			Voice: request.Voice, Speed: request.Speed,
			Chars:      utf8.RuneCountInString(strings.TrimSpace(request.Text)),
			DurationMS: time.Since(started).Milliseconds(), Cached: summary.Cached,
			Text: truncateRunes(strings.TrimSpace(request.Text), 500),
		}); err != nil {
			log.Printf("tts: speech log: %v", err)
		}
	}
	return summary, nil
}

func truncateRunes(text string, limit int) string {
	value := []rune(text)
	if len(value) <= limit {
		return text
	}
	return string(value[:limit])
}

// WriteStreamingWAV writes a streaming RIFF header with unknown final sizes.
func WriteStreamingWAV(w io.Writer, sampleRate, channels int) error {
	header := make([]byte, 44)
	copy(header, "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], ^uint32(0))
	copy(header[8:12], "WAVEfmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1)
	binary.LittleEndian.PutUint16(header[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(header[24:28], uint32(sampleRate))
	byteRate := sampleRate * channels * 2
	binary.LittleEndian.PutUint32(header[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(header[32:34], uint16(channels*2))
	binary.LittleEndian.PutUint16(header[34:36], 16)
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], ^uint32(0))
	_, err := w.Write(header)
	return err
}
