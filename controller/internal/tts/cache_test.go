package tts

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSentenceCacheHitMissAtomicAndCorruption(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "cache")
	runner := &fixtureRunner{}
	service := NewService(Config{
		SherpaDir: "/sherpa", ModelDir: "/models", CacheDir: cacheDir,
		CacheMaxBytes: 1024 * 1024, Runner: runner,
	})
	for index := 0; index < 2; index++ {
		pcm, rate, channels, cached, err := service.synthesize(
			context.Background(), "Exact sentence.", DefaultVoice, 1,
		)
		if err != nil || len(pcm) != 8 || rate != 22050 || channels != 1 || cached != (index == 1) {
			t.Fatalf("synthesize %d = %d, %d, %d, %v, %v", index, len(pcm), rate, channels, cached, err)
		}
	}
	if len(runner.args) != 1 {
		t.Fatalf("runner calls after hit = %d", len(runner.args))
	}
	items, err := os.ReadDir(cacheDir)
	if err != nil || len(items) != 1 || filepath.Ext(items[0].Name()) != ".wav" {
		t.Fatalf("cache files = %#v, %v", items, err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, items[0].Name()), []byte("RIFF"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, _, cached, err := service.synthesize(context.Background(), "Exact sentence.", DefaultVoice, 1)
	if err != nil || cached || len(runner.args) != 2 {
		t.Fatalf("corrupt rewrite = cached %v, calls %d, err %v", cached, len(runner.args), err)
	}
	items, _ = os.ReadDir(cacheDir)
	for _, item := range items {
		if strings.HasSuffix(item.Name(), ".tmp") {
			t.Fatalf("atomic write left temporary file %q", item.Name())
		}
	}
}

func TestSentenceCacheEvictsLeastRecentlyUsed(t *testing.T) {
	cacheDir := t.TempDir()
	runner := &fixtureRunner{}
	service := NewService(Config{
		ModelDir: "/models", CacheDir: cacheDir,
		CacheMaxBytes: int64(len(wavFixture(false)) + 4), Runner: runner,
	})
	_, _, _, _, err := service.synthesize(context.Background(), "Old sentence.", DefaultVoice, 1)
	if err != nil {
		t.Fatal(err)
	}
	oldPath := service.cachePath(DefaultVoice, 1, "Old sentence.")
	oldTime := time.Now().Add(-time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, err = service.synthesize(context.Background(), "New sentence.", DefaultVoice, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old cache entry still exists: %v", err)
	}
	if _, err := os.Stat(service.cachePath(DefaultVoice, 1, "New sentence.")); err != nil {
		t.Fatalf("new cache entry missing: %v", err)
	}
}

func TestVoiceWhitelistAndFallback(t *testing.T) {
	if NormalizeVoice("en_US-ryan-medium") != "en_US-ryan-medium" {
		t.Fatal("Ryan voice rejected")
	}
	if NormalizeVoice("unknown") != DefaultVoice {
		t.Fatal("unknown voice did not fall back")
	}
	runner := &fixtureRunner{}
	service := NewService(Config{ModelDir: "/models", Runner: runner})
	_, _, _, _, err := service.synthesize(context.Background(), "Ryan sentence.", "en_US-ryan-medium", 1)
	if err != nil {
		t.Fatal(err)
	}
	argv := strings.Join(runner.args[0], " ")
	if !strings.Contains(argv, "/vits-piper-en_US-ryan-medium/en_US-ryan-medium.onnx") {
		t.Fatalf("Ryan model path missing from %q", argv)
	}
}
