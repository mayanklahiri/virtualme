// Command controller wires the Virtual Me control plane.
package main

import (
	"context"
	"encoding/json"
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
	"syscall"
	"time"

	assets "github.com/mayanklahiri/virtualme/controller"
	"github.com/mayanklahiri/virtualme/controller/internal/agent"
	"github.com/mayanklahiri/virtualme/controller/internal/chat"
	"github.com/mayanklahiri/virtualme/controller/internal/health"
	"github.com/mayanklahiri/virtualme/controller/internal/metrics"
	"github.com/mayanklahiri/virtualme/controller/internal/state"
	"github.com/mayanklahiri/virtualme/controller/internal/ws"
)

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

func spaHandler(staticFS fs.FS) http.Handler {
	files := http.FileServer(http.FS(staticFS))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/healthz") || strings.HasPrefix(r.URL.Path, "/ws") ||
			strings.HasPrefix(r.URL.Path, "/desktop/") {
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

func newMux(cfg health.Config, hub *ws.Hub, desktopURL *url.URL) *http.ServeMux {
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
	mux.Handle("/desktop/", http.StripPrefix("/desktop/", httputil.NewSingleHostReverseProxy(desktopURL)))
	staticFS, err := fs.Sub(assets.WebFS, "web/dist")
	if err != nil {
		panic(err)
	}
	mux.Handle("/", spaHandler(staticFS))
	return mux
}

func main() {
	log.SetOutput(os.Stdout)
	log.SetFlags(0)
	cfg := health.FromEnv()
	hub := ws.NewHub()
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
	collector := state.NewCollector(cfg, "/proc", metricsStore, hub.Broadcast)
	chatService := chat.NewWithAgent(
		cfg.ValkeyAddr,
		"http://127.0.0.1:8081/v1/chat/completions",
		hub.Broadcast,
		agent.Config{
			CDPURL:      "http://127.0.0.1:9222",
			Display:     envOr("VM_DISPLAY", ":99"),
			Resolution:  envOr("VM_RESOLUTION", "1600x900x24"),
			XdotoolPath: "xdotool",
			ScrotPath:   "scrot",
			ConvertPath: "convert",
			BashPath:    "bash",
			DataDir:     path.Join(dataDir, "agent"),
			Manifest:    "/opt/agent/system-manifest.json",
			MaxSteps:    envInt("VM_AGENT_MAX_STEPS", 25),
			KeepTasks:   envInt("VM_AGENT_KEEP_TASKS", 20),
		},
	)
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
		chatService.HandleClientMessage(conn, payload)
	})
	hub.SetOnConnect(func(conn *ws.Conn) {
		_ = conn.WriteText(chatService.HistoryMessage())
		_ = conn.WriteText(chatService.StatsMessage())
	})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go collector.Run(ctx)
	persistDone := make(chan struct{})
	go func() {
		metricsStore.RunPersist(ctx)
		close(persistDone)
	}()
	addr := os.Getenv("VM_HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	log.Println("health: six concurrent probes configured")
	log.Println("websocket: state hub ready (chat + metrics wired)")
	log.Println("chat: llama-backed shared conversation ready")
	log.Println("agent: OS-level browser-control loop ready")
	log.Println("state: collector started (2s, tiered metrics)")
	log.Println("desktop: proxying", desktopURL)
	log.Println("controller: listening on", addr)
	server := &http.Server{Addr: addr, Handler: newMux(cfg, hub, desktopURL), ReadHeaderTimeout: 5 * time.Second}
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
