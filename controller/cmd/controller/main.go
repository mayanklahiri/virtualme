// Command controller wires the Virtual Me control plane.
package main

import (
	"context"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"

	assets "github.com/mayanklahiri/virtualme/controller"
	"github.com/mayanklahiri/virtualme/controller/internal/chat"
	"github.com/mayanklahiri/virtualme/controller/internal/health"
	"github.com/mayanklahiri/virtualme/controller/internal/state"
	"github.com/mayanklahiri/virtualme/controller/internal/ws"
)

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
	mux.Handle("/", http.FileServer(http.FS(staticFS)))
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
	collector := state.NewCollector(cfg, "/proc", hub.Broadcast)
	chatService := chat.New(cfg.ValkeyAddr, "http://127.0.0.1:8081/v1/chat/completions", hub.Broadcast)
	chatService.LoadHistory()
	hub.SetHandler(chatService.HandleClientMessage)
	hub.SetOnConnect(func(conn *ws.Conn) {
		_ = conn.WriteText(collector.HistoryMessage())
		_ = conn.WriteText(chatService.HistoryMessage())
	})
	go collector.Run(context.Background())
	addr := os.Getenv("VM_HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	log.Println("health: six concurrent probes configured")
	log.Println("websocket: state hub ready (chat + replay wired)")
	log.Println("chat: llama-backed shared conversation ready")
	log.Println("state: collector started (2s, ring buffer 150)")
	log.Println("desktop: proxying", desktopURL)
	log.Println("controller: listening on", addr)
	log.Fatal(http.ListenAndServe(addr, newMux(cfg, hub, desktopURL)))
}
