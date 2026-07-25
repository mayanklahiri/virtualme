// Command controller wires the Virtual Me control plane.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"path"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	assets "github.com/mayanklahiri/virtualme/controller"
	"github.com/mayanklahiri/virtualme/controller/internal/agent"
	"github.com/mayanklahiri/virtualme/controller/internal/chat"
	"github.com/mayanklahiri/virtualme/controller/internal/gpu"
	"github.com/mayanklahiri/virtualme/controller/internal/health"
	"github.com/mayanklahiri/virtualme/controller/internal/jiggler"
	"github.com/mayanklahiri/virtualme/controller/internal/jobs"
	"github.com/mayanklahiri/virtualme/controller/internal/mail"
	"github.com/mayanklahiri/virtualme/controller/internal/metrics"
	"github.com/mayanklahiri/virtualme/controller/internal/projects"
	"github.com/mayanklahiri/virtualme/controller/internal/state"
	"github.com/mayanklahiri/virtualme/controller/internal/tts"
	"github.com/mayanklahiri/virtualme/controller/internal/valkey"
	"github.com/mayanklahiri/virtualme/controller/internal/ws"
)

var version = "dev"

func envInt(name string, fallback int) int {
	if value, err := strconv.Atoi(os.Getenv(name)); err == nil && value > 0 {
		return value
	}
	return fallback
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func capText(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	const suffix = "\n…[truncated]"
	return text[:limit-len(suffix)] + suffix
}

func jsonObject(raw json.RawMessage) bool {
	var value map[string]any
	return len(raw) > 0 && json.Unmarshal(raw, &value) == nil && value != nil
}

func envResolution() (int, int) {
	parts := strings.Split(envOr("VM_RESOLUTION", "1600x900x24"), "x")
	if len(parts) < 2 {
		return 1600, 900
	}
	width, widthErr := strconv.Atoi(parts[0])
	height, heightErr := strconv.Atoi(parts[1])
	if widthErr != nil || heightErr != nil || width < 5 || height < 5 {
		return 1600, 900
	}
	return width, height
}

func spaHandler(staticFS fs.FS) http.Handler {
	files := http.FileServer(http.FS(staticFS))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/healthz") || strings.HasPrefix(r.URL.Path, "/ws") ||
			strings.HasPrefix(r.URL.Path, "/desktop/") || strings.HasPrefix(r.URL.Path, "/v1/audio/speech") {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "." || name == "" {
			name = "index.html"
		}
		if _, err := fs.Stat(staticFS, name); err == nil {
			files.ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodGet && path.Ext(name) == "" {
			content, err := fs.ReadFile(staticFS, "index.html")
			if err != nil {
				http.Error(w, "SPA unavailable", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", mime.TypeByExtension(".html"))
			_, _ = w.Write(content)
			return
		}
		http.NotFound(w, r)
	})
}

func newMux(cfg health.Config, hub *ws.Hub, desktopURL *url.URL, clients ...*tts.Client) *http.ServeMux {
	return newMuxWithActivity(cfg, hub, desktopURL, nil, clients...)
}

func newMuxWithActivity(cfg health.Config, hub *ws.Hub, desktopURL *url.URL, activity *jobs.Activity, clients ...*tts.Client) *http.ServeMux {
	client := &tts.Client{URL: "http://127.0.0.1:" + envOr("VM_TTS_PORT", "8082")}
	if len(clients) > 0 && clients[0] != nil {
		client = clients[0]
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		report := health.Gather(cfg)
		w.Header().Set("Content-Type", "application/json")
		if !report.OK {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(report)
	})
	mux.HandleFunc("/ws", hub.HandleUpgrade)
	mux.HandleFunc("/v1/audio/speech", speechHandler(client, activity))
	desktopProxy := http.StripPrefix("/desktop/", httputil.NewSingleHostReverseProxy(desktopURL))
	redirectDesktop := func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && (r.URL.Path == "/desktop" || r.URL.Path == "/desktop/") {
			http.Redirect(w, r, "/desktop/vnc.html?autoconnect=1&resize=scale&path=desktop/websockify", http.StatusFound)
			return
		}
		desktopProxy.ServeHTTP(w, r)
	}
	mux.HandleFunc("/desktop", redirectDesktop)
	mux.HandleFunc("/desktop/", redirectDesktop)
	staticFS, err := fs.Sub(assets.WebFS, "web/dist")
	if err != nil {
		panic(err)
	}
	mux.Handle("/", spaHandler(staticFS))
	return mux
}

func openAIError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{
		"message": message, "type": "invalid_request_error",
	}})
}

func speechHandler(client *tts.Client, activities ...jobs.ActivityRecorder) http.HandlerFunc {
	var activity jobs.ActivityRecorder
	if len(activities) > 0 {
		activity = activities[0]
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var input struct {
			Model          string  `json:"model"`
			Input          string  `json:"input"`
			Voice          string  `json:"voice"`
			ResponseFormat string  `json:"response_format"`
			Speed          float64 `json:"speed"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			openAIError(w, http.StatusBadRequest, "invalid request: "+err.Error())
			return
		}
		input.Input = strings.TrimSpace(input.Input)
		if input.Input == "" || len([]rune(input.Input)) > 4096 {
			openAIError(w, http.StatusBadRequest, "input must be 1-4096 characters")
			return
		}
		if input.ResponseFormat == "" {
			input.ResponseFormat = "wav"
		}
		if input.ResponseFormat != "wav" && input.ResponseFormat != "pcm" {
			openAIError(w, http.StatusBadRequest, "response_format must be wav or pcm")
			return
		}
		voice := tts.NormalizeVoice(input.Voice)
		w.Header().Set("X-VM-Voice", voice)
		startedAt := time.Now()
		started := false
		_, err := client.Synthesize(r.Context(), tts.Request{
			Text: input.Input, Speed: input.Speed, Voice: voice, Origin: "api",
		}, func(event tts.Event) error {
			switch event.Type {
			case "start":
				started = true
				w.Header().Set("X-VM-Sample-Rate", strconv.Itoa(event.SampleRate))
				if input.ResponseFormat == "pcm" {
					w.Header().Set("Content-Type", "audio/pcm")
				} else {
					w.Header().Set("Content-Type", "audio/wav")
					if err := tts.WriteStreamingWAV(w, event.SampleRate, event.Channels); err != nil {
						return err
					}
				}
				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}
			case "chunk":
				pcm, err := base64.StdEncoding.DecodeString(event.PCM)
				if err != nil {
					return err
				}
				if _, err := w.Write(pcm); err != nil {
					return err
				}
				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}
			}
			return nil
		})
		if err != nil && !started {
			openAIError(w, http.StatusBadGateway, err.Error())
		}
		if activity != nil {
			_ = activity.Record(jobs.ActivityEvent{
				Kind: "tts", Name: "synthesize",
				Summary: fmt.Sprintf("Synthesized %d characters with %s", len([]rune(input.Input)), voice),
				Detail: jobs.ActivityDetail{
					DurationMS: time.Since(startedAt).Milliseconds(), OK: err == nil,
					Chars: len([]rune(input.Input)), Voice: voice,
				},
			})
		}
	}
}

type ttsWS struct {
	client   *tts.Client
	activity jobs.ActivityRecorder
	mu       sync.Mutex
	next     uint64
	active   map[*ws.Conn]ttsFlight
}

type ttsFlight struct {
	id     string
	token  uint64
	cancel context.CancelFunc
}

func newTTSWS(client *tts.Client, activities ...jobs.ActivityRecorder) *ttsWS {
	var activity jobs.ActivityRecorder
	if len(activities) > 0 {
		activity = activities[0]
	}
	return &ttsWS{client: client, activity: activity, active: make(map[*ws.Conn]ttsFlight)}
}

func (t *ttsWS) start(conn *ws.Conn, id string) (context.Context, context.CancelFunc, uint64, string) {
	ctx, cancel := context.WithCancel(context.Background())
	t.mu.Lock()
	old := t.active[conn]
	t.next++
	token := t.next
	t.active[conn] = ttsFlight{id: id, token: token, cancel: cancel}
	t.mu.Unlock()
	if old.cancel != nil {
		old.cancel()
	}
	return ctx, cancel, token, old.id
}

func (t *ttsWS) stop(conn *ws.Conn, id string) bool {
	t.mu.Lock()
	flight, ok := t.active[conn]
	if ok && flight.id == id {
		delete(t.active, conn)
	} else {
		ok = false
	}
	t.mu.Unlock()
	if ok {
		flight.cancel()
	}
	return ok
}

func (t *ttsWS) done(conn *ws.Conn, token uint64) {
	t.mu.Lock()
	if flight, ok := t.active[conn]; ok && flight.token == token {
		delete(t.active, conn)
	}
	t.mu.Unlock()
}

func (t *ttsWS) handle(conn *ws.Conn, payload []byte) bool {
	var request struct {
		Type  string  `json:"type"`
		ID    string  `json:"id"`
		Text  string  `json:"text"`
		Speed float64 `json:"speed"`
		Voice string  `json:"voice"`
	}
	if json.Unmarshal(payload, &request) != nil || (request.Type != "tts-req" && request.Type != "tts-stop") {
		return false
	}
	if request.Type == "tts-stop" {
		if t.stop(conn, request.ID) {
			t.write(conn, map[string]any{"type": "tts-status", "id": request.ID, "origin": "console", "phase": "stopped"})
		}
		return true
	}
	ctx, cancel, token, oldID := t.start(conn, request.ID)
	if oldID != "" {
		t.write(conn, map[string]any{"type": "tts-status", "id": oldID, "origin": "console", "phase": "stopped"})
	}
	go func() {
		voice := tts.NormalizeVoice(request.Voice)
		startedAt := time.Now()
		defer cancel()
		defer t.done(conn, token)
		t.write(conn, map[string]any{"type": "tts-status", "id": request.ID, "origin": "console", "phase": "queued"})
		sentences := 0
		_, err := t.client.Synthesize(ctx, tts.Request{
			Text: request.Text, Speed: request.Speed, Voice: voice, Origin: "console",
		}, func(event tts.Event) error {
			frame := map[string]any{"id": request.ID, "origin": "console"}
			switch event.Type {
			case "start":
				sentences = event.Sentences
				frame["type"], frame["sampleRate"], frame["channels"], frame["sentences"] = "tts-start", event.SampleRate, event.Channels, event.Sentences
			case "chunk":
				frame["type"], frame["seq"], frame["pcm"] = "tts-chunk", event.Seq, event.PCM
				t.write(conn, map[string]any{"type": "tts-status", "id": request.ID, "origin": "console", "phase": "synthesizing", "sentence": event.Seq + 1, "sentences": sentences})
			case "done":
				frame["type"], frame["audioSec"], frame["rtf"], frame["cached"] = "tts-done", event.AudioSec, event.RTF, event.Cached
				t.write(conn, map[string]any{"type": "tts-status", "id": request.ID, "origin": "console", "phase": "done", "sentences": sentences, "rtf": event.RTF})
			default:
				return nil
			}
			return t.write(conn, frame)
		})
		if err != nil && ctx.Err() == nil {
			t.write(conn, map[string]any{"type": "tts-error", "id": request.ID, "origin": "console", "error": err.Error()})
			t.write(conn, map[string]any{"type": "tts-status", "id": request.ID, "origin": "console", "phase": "failed"})
		}
		if t.activity != nil {
			_ = t.activity.Record(jobs.ActivityEvent{
				Kind: "tts", Name: "synthesize",
				Summary: fmt.Sprintf("Synthesized %d characters with %s", len([]rune(request.Text)), voice),
				Detail: jobs.ActivityDetail{
					DurationMS: time.Since(startedAt).Milliseconds(), OK: err == nil,
					Chars: len([]rune(request.Text)), Voice: voice,
				},
			})
		}
	}()
	return true
}

func (t *ttsWS) write(conn *ws.Conn, value any) error {
	payload, _ := json.Marshal(value)
	return conn.WriteText(payload)
}

func main() {
	log.SetOutput(os.Stdout)
	log.SetFlags(0)
	cfg := health.FromEnv()
	hub := ws.NewHub()
	activity := jobs.NewActivity(valkey.New(cfg.ValkeyAddr), hub.Broadcast)
	speechLog := tts.NewLog(valkey.New(cfg.ValkeyAddr), hub.Broadcast)
	ttsClient := &tts.Client{
		URL: "http://127.0.0.1:" + envOr("VM_TTS_PORT", "8082"),
		Log: speechLog,
	}
	ttsSocket := newTTSWS(ttsClient, activity)
	desktopURL, err := url.Parse("http://127.0.0.1:6080")
	if err != nil {
		log.Fatal(err)
	}
	dataDir := os.Getenv("VM_DATA_DIR")
	if dataDir == "" {
		dataDir = os.TempDir()
	}
	metricsStore := metrics.NewStore(path.Join(dataDir, "metrics"))
	metricsStore.Load()
	mailname := os.Getenv("VM_MAIL_MAILNAME")
	if mailname == "" {
		mailname, _ = os.Hostname()
	}
	mailService, err := mail.NewService(mail.Config{
		DataDir: dataDir, SendmailPath: cfg.SendmailPath,
		Mailname: mailname, From: os.Getenv("VM_MAIL_FROM"),
		Smarthost:     os.Getenv("VM_MAIL_SMARTHOST"),
		DKIMDomain:    os.Getenv("VM_MAIL_DKIM_DOMAIN"),
		DKIMSelector:  envOr("VM_MAIL_DKIM_SELECTOR", "virtualme"),
		FlushEverySec: int64(envInt("VM_MAIL_FLUSH_SEC", 60)),
		Broadcast:     hub.Broadcast,
		Activity:      activity,
	})
	if err != nil {
		log.Fatal(err)
	}
	llmCounters := new(metrics.Counters)
	agentConfig := agent.Config{
		CDPURL:        "http://127.0.0.1:9222",
		Display:       envOr("VM_DISPLAY", ":99"),
		Resolution:    envOr("VM_RESOLUTION", "1600x900x24"),
		XdotoolPath:   "xdotool",
		ScrotPath:     "scrot",
		ConvertPath:   "convert",
		BashPath:      "bash",
		DataDir:       path.Join(dataDir, "agent"),
		Manifest:      "/opt/agent/system-manifest.json",
		MaxSteps:      envInt("VM_AGENT_MAX_STEPS", 25),
		KeepTasks:     envInt("VM_AGENT_KEEP_TASKS", 20),
		ContextTokens: envInt("VM_LLAMA_CTX", 16384),
		TTS:           ttsClient,
		Activity:      activity,
		Broadcast:     hub.Broadcast,
		Counters:      llmCounters,
	}
	localTools := agent.NewLocalTools(agentConfig)
	agentConfig.Executor = localTools
	chatService := chat.NewWithAgent(
		cfg.ValkeyAddr,
		"http://127.0.0.1:8081/v1/chat/completions",
		hub.Broadcast,
		agentConfig,
	)
	chatService.SetActivity(activity)
	chatService.SetCounters(llmCounters)
	jobManager := jobs.New(valkey.New(cfg.ValkeyAddr), hub.Broadcast)
	chatService.SetJobManager(jobManager)
	jobManager.Register("chat", chatService.Execute)
	width, height := envResolution()
	jigglerService := jiggler.New(agent.NewProcessRunner(), valkey.New(cfg.ValkeyAddr), hub.Broadcast, width, height)
	jigglerService.SetDisplay(envOr("VM_DISPLAY", ":99"))
	jigglerService.SetActivity(activity)
	gpuInfo := gpu.Detect()
	addr := envOr("VM_HTTP_ADDR", ":8080")
	collector := state.NewCollector(
		cfg, "/proc", metricsStore, hub.Broadcast, gpuInfo, jigglerService.Enabled,
		state.Runtime{Version: version, HTTPAddr: addr},
	)
	collector.SetCounters(llmCounters)
	collector.SetSchedulerPaused(jobManager.SchedulerPaused)
	projectService := projects.New(
		valkey.New(cfg.ValkeyAddr), jobManager, chatService, dataDir, hub.Broadcast,
	)
	toolsList, err := json.Marshal(map[string]any{"type": "tools-list", "tools": localTools.Manifest()})
	if err != nil {
		log.Fatal("tools: invalid manifest:", err)
	}
	jobManager.Register("manual-tool", func(ctx context.Context, env jobs.Envelope) (string, error) {
		var request struct {
			ID   string          `json:"id"`
			Tool string          `json:"tool"`
			Args json.RawMessage `json:"args"`
		}
		if err := json.Unmarshal(env.Payload, &request); err != nil {
			return "", err
		}
		started := time.Now()
		result, toolErr := localTools.Execute(ctx, request.Tool, request.Args)
		duration := time.Since(started).Milliseconds()
		llmCounters.AddAction(agent.ActionCategory(request.Tool, result.Observe))
		text := capText(result.Text, 16*1024)
		errorText := ""
		if toolErr != nil {
			errorText = toolErr.Error()
		}
		image := ""
		if len(result.ImageJPEG) > 0 {
			image = "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(result.ImageJPEG)
		}
		reply, _ := json.Marshal(map[string]any{
			"type": "tool-result", "id": request.ID, "ok": toolErr == nil,
			"durationMs": duration, "text": text, "image": image, "error": errorText,
		})
		hub.SendTo(env.InitiatorConn, reply)
		summary := result.Summary
		if summary == "" {
			summary = request.Tool
		}
		var args any
		if json.Unmarshal(request.Args, &args) != nil {
			args = string(request.Args)
		}
		_ = activity.Record(jobs.ActivityEvent{
			Kind: "tool", Name: request.Tool, JobID: env.ID, Summary: summary,
			Detail: jobs.ActivityDetail{
				Args: args, ResultText: text, DurationMS: duration, OK: toolErr == nil,
			},
		})
		if toolErr != nil {
			return "Tool failed: " + toolErr.Error(), nil
		}
		return summary, nil
	})
	jobManager.Register("soak-probe", func(ctx context.Context, env jobs.Envelope) (string, error) {
		var payload struct {
			Echo string `json:"echo"`
		}
		if err := json.Unmarshal(env.Payload, &payload); err != nil {
			return "", err
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Second):
			return payload.Echo, nil
		}
	})
	go chatService.LoadHistory()
	hub.SetHandler(func(conn *ws.Conn, payload []byte) {
		var request struct {
			Type     string `json:"type"`
			Lookback string `json:"lookback"`
		}
		if json.Unmarshal(payload, &request) == nil && request.Type == "metrics-req" {
			resSec, samples, ok := metricsStore.Query(request.Lookback)
			if !ok {
				_ = conn.WriteText([]byte(`{"type":"chat-error","error":"invalid metrics lookback"}`))
				return
			}
			reply, _ := json.Marshal(struct {
				Type     string           `json:"type"`
				Lookback string           `json:"lookback"`
				ResSec   int              `json:"resSec"`
				Samples  []metrics.Sample `json:"samples"`
			}{Type: "metrics", Lookback: request.Lookback, ResSec: resSec, Samples: samples})
			_ = conn.WriteText(reply)
			return
		}
		if ttsSocket.handle(conn, payload) {
			return
		}
		if speechLog.HandleMessage(payload, conn.WriteText) {
			return
		}
		if mailService.Handle(payload, conn.WriteText) {
			return
		}
		if json.Unmarshal(payload, &request) == nil && request.Type == "tools-list-req" {
			_ = conn.WriteText(toolsList)
			return
		}
		if json.Unmarshal(payload, &request) == nil && request.Type == "tool-invoke" {
			var invoke struct {
				ID   string          `json:"id"`
				Tool string          `json:"tool"`
				Args json.RawMessage `json:"args"`
			}
			if err := json.Unmarshal(payload, &invoke); err != nil || invoke.ID == "" ||
				!localTools.Has(invoke.Tool) || !jsonObject(invoke.Args) {
				message := "invalid tool invocation"
				if invoke.Tool != "" && !localTools.Has(invoke.Tool) {
					message = "unknown tool " + strconv.Quote(invoke.Tool)
				}
				reply, _ := json.Marshal(map[string]any{
					"type": "tool-result", "id": invoke.ID, "ok": false,
					"durationMs": 0, "text": "", "image": "", "error": message,
				})
				_ = conn.WriteText(reply)
				return
			}
			jobPayload, _ := json.Marshal(map[string]any{"id": invoke.ID, "tool": invoke.Tool, "args": invoke.Args})
			if _, err := jobManager.Enqueue(jobs.Envelope{
				ID: jobs.NewID(), Type: "manual-tool", Payload: jobPayload, Priority: "interactive",
				InitiatorConn: conn.ID(), VisibilityTimeoutSec: 300,
			}); err != nil {
				reply, _ := json.Marshal(map[string]any{
					"type": "tool-result", "id": invoke.ID, "ok": false,
					"durationMs": 0, "text": "", "image": "", "error": "tool enqueue failed: " + err.Error(),
				})
				_ = conn.WriteText(reply)
			}
			return
		}
		if jobManager.HandleMessage(conn, payload) {
			return
		}
		if projectService.HandleMessage(conn, payload) {
			return
		}
		if activity.HandleMessage(conn, payload) {
			return
		}
		if jigglerService.HandleMessage(payload) {
			return
		}
		chatService.HandleClientMessage(conn, payload)
	})
	hub.SetOnConnect(func(conn *ws.Conn) {
		_ = conn.WriteText(chatService.HistoryMessage())
		for _, frame := range chatService.AgentReplayFrames() {
			_ = conn.WriteText(frame)
		}
		_ = conn.WriteText(chatService.StatsMessage())
		_ = conn.WriteText(mailService.StatusMessage())
		_ = conn.WriteText(jobManager.StateMessage())
		_ = conn.WriteText(projectService.Message())
		_ = conn.WriteText(activity.Message())
		_ = conn.WriteText(speechLog.Message())
		_ = conn.WriteText(toolsList)
	})
	hub.SetOnDisconnect(jobManager.DropInitiator)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go mailService.Start(ctx, func() bool { return hub.Count() > 0 })
	if err := jobManager.Start(ctx); err != nil {
		log.Fatal("jobs: startup failed:", err)
	}
	if err := jigglerService.Start(ctx); err != nil {
		log.Fatal("jiggler: startup failed:", err)
	}
	go collector.Run(ctx)
	persistDone := make(chan struct{})
	go func() {
		metricsStore.RunPersist(ctx)
		close(persistDone)
	}()
	log.Println("health: eight concurrent probes configured")
	log.Println("websocket: state hub ready (chat + metrics + tts + mail + jobs + projects wired)")
	log.Println("chat: queued llama-backed shared conversation ready")
	log.Println("jobs: sequential worker and scheduler ready")
	log.Println("agent: OS-level browser-control loop ready")
	log.Println("jiggler: ambient mouse service ready")
	log.Println("state: collector started (2s, tiered metrics)")
	log.Println("desktop: proxying", desktopURL)
	log.Println("controller: listening on", addr)
	server := &http.Server{Addr: addr, Handler: newMuxWithActivity(cfg, hub, desktopURL, activity, ttsClient), ReadHeaderTimeout: 5 * time.Second}
	errs := make(chan error, 1)
	go func() { errs <- server.ListenAndServe() }()
	select {
	case err := <-errs:
		if err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		<-errs
		<-persistDone
	}
}
