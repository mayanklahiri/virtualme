// Command ttsd exposes the pinned local TTS engine over loopback.
package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/mayanklahiri/virtualme/controller/internal/config"
	"github.com/mayanklahiri/virtualme/controller/internal/tts"
)

func main() {
	log.SetOutput(os.Stdout)
	log.SetFlags(0)
	loaded, err := config.Load(config.Options{})
	if err != nil {
		log.Fatal(err)
	}
	cfg := loaded.Config.TTS
	service := tts.NewService(tts.Config{
		SherpaDir:     cfg.SherpaDirectory,
		ModelDir:      cfg.ModelDirectory,
		CacheDir:      cfg.CacheDirectory,
		CacheMaxBytes: cfg.CacheMaxMiB * 1024 * 1024,
		MaxChars:      cfg.MaxCharacters,
	})
	addr := cfg.Address
	log.Println("ttsd: available voices:", service.AvailableVoices())
	log.Println("ttsd: listening on", addr)
	server := &http.Server{Addr: addr, Handler: service.Handler(), ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(server.ListenAndServe())
}
