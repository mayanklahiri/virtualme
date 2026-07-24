package tts

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

func cacheKey(voice string, speed float64, sentence string) string {
	sum := sha256.Sum256([]byte(voice + "\x00" +
		strconv.FormatFloat(speed, 'g', -1, 64) + "\x00" + sentence))
	return hex.EncodeToString(sum[:])
}

func (s *Service) cachePath(voice string, speed float64, sentence string) string {
	return filepath.Join(s.cfg.CacheDir, cacheKey(voice, speed, sentence)+".wav")
}

func (s *Service) readCache(voice string, speed float64, sentence string) ([]byte, int, int, bool) {
	name := s.cachePath(voice, speed, sentence)
	pcm, rate, channels, err := ReadWAV(name)
	if err != nil {
		if !os.IsNotExist(err) {
			_ = os.Remove(name)
		}
		return nil, 0, 0, false
	}
	now := time.Now()
	_ = os.Chtimes(name, now, now)
	return pcm, rate, channels, true
}

func (s *Service) writeCache(voice string, speed float64, sentence, source string) error {
	if err := os.MkdirAll(s.cfg.CacheDir, 0o700); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.CreateTemp(s.cfg.CacheDir, ".tts-cache-*.tmp")
	if err != nil {
		return err
	}
	temp := output.Name()
	defer os.Remove(temp)
	if _, err = io.Copy(output, input); err == nil {
		err = output.Sync()
	}
	if closeErr := output.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Chmod(temp, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temp, s.cachePath(voice, speed, sentence)); err != nil {
		return err
	}
	return s.evictCache()
}

func (s *Service) evictCache() error {
	if s.cfg.CacheMaxBytes <= 0 {
		return nil
	}
	items, err := os.ReadDir(s.cfg.CacheDir)
	if err != nil {
		return err
	}
	type cachedFile struct {
		name    string
		size    int64
		modTime time.Time
	}
	files := make([]cachedFile, 0, len(items))
	var total int64
	for _, item := range items {
		if item.IsDir() || filepath.Ext(item.Name()) != ".wav" {
			continue
		}
		info, statErr := item.Info()
		if statErr != nil {
			continue
		}
		files = append(files, cachedFile{item.Name(), info.Size(), info.ModTime()})
		total += info.Size()
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].modTime.Equal(files[j].modTime) {
			return files[i].name < files[j].name
		}
		return files[i].modTime.Before(files[j].modTime)
	})
	for _, file := range files {
		if total <= s.cfg.CacheMaxBytes {
			break
		}
		if err := os.Remove(filepath.Join(s.cfg.CacheDir, file.name)); err != nil {
			return err
		}
		total -= file.size
	}
	return nil
}
