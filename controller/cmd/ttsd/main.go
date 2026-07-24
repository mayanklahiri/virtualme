// Command ttsd exposes the pinned local TTS engine over loopback.
package main

import (
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/mayanklahiri/virtualme/controller/internal/tts"
)

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func main() {
	log.SetOutput(os.Stdout)
	log.SetFlags(0)
	maxChars, err := strconv.Atoi(env("VM_TTS_MAX_CHARS", "4096"))
	if err != nil || maxChars <= 0 {
		log.Fatal("invalid VM_TTS_MAX_CHARS")
	}
	cacheMaxMB, err := strconv.ParseInt(env("VM_TTS_CACHE_MAX_MB", "256"), 10, 64)
	if err != nil || cacheMaxMB <= 0 {
		log.Fatal("invalid VM_TTS_CACHE_MAX_MB")
	}
	dataDir := env("VM_DATA_DIR", "/home/virtualme/.virtualme")
	service := tts.NewService(tts.Config{
		SherpaDir:     env("VM_SHERPA_DIR", "/opt/sherpa-onnx"),
		ModelDir:      env("VM_TTS_MODEL_DIR", "/opt/models/tts"),
		CacheDir:      env("VM_TTS_CACHE_DIR", dataDir+"/tts-cache"),
		CacheMaxBytes: cacheMaxMB * 1024 * 1024,
		MaxChars:      maxChars,
	})
	addr := "127.0.0.1:" + env("VM_TTS_PORT", "8082")
	log.Println("ttsd: available voices:", service.AvailableVoices())
	log.Println("ttsd: listening on", addr)
	server := &http.Server{Addr: addr, Handler: service.Handler(), ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(server.ListenAndServe())
}
