// Package procstat samples per-service CPU and memory usage from /proc.
package procstat

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Proc is one service's aggregated resource usage.
type Proc struct {
	Name   string  `json:"name"`
	CPUPct float64 `json:"cpuPct"`
	MemMB  int     `json:"memMB"`
}

// services maps /proc comm values to service names in fixed output order.
// Chromium is matched by prefix so renderer/gpu children aggregate with it.
var services = []struct {
	name   string
	comm   string
	prefix bool
}{
	{"xvfb", "Xvfb", false},
	{"openbox", "openbox", false},
	{"x11vnc", "x11vnc", false},
	{"novnc", "websockify", false},
	{"valkey", "valkey-server", false},
	{"llama", "llama-server", false},
	{"chromium", "chromium", true},
	{"controller", "controller", false},
}

// Sampler computes CPU% deltas between successive Sample calls.
type Sampler struct {
	procRoot string
	pageSize int
	hertz    float64
	prev     map[int]uint64
	prevTime time.Time
}

// NewSampler returns a sampler rooted at procRoot (normally "/proc").
func NewSampler(procRoot string) *Sampler {
	return &Sampler{
		procRoot: procRoot,
		pageSize: os.Getpagesize(),
		hertz:    100, // sysconf(_SC_CLK_TCK) on Linux
		prev:     make(map[int]uint64),
	}
}

// parseStat extracts utime+stime ticks from /proc/<pid>/stat content,
// parsing after the last ')' to survive spaces or parens in comm.
func parseStat(content string) (ticks uint64, ok bool) {
	end := strings.LastIndexByte(content, ')')
	if end < 0 {
		return 0, false
	}
	fields := strings.Fields(content[end+1:])
	// After ')' the fields are: state ppid pgrp ... utime(idx 11) stime(idx 12).
	if len(fields) < 13 {
		return 0, false
	}
	utime, err1 := strconv.ParseUint(fields[11], 10, 64)
	stime, err2 := strconv.ParseUint(fields[12], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, false
	}
	return utime + stime, true
}

// parseStatm extracts RSS pages (field 2) from /proc/<pid>/statm content.
func parseStatm(content string) (rssPages uint64, ok bool) {
	fields := strings.Fields(content)
	if len(fields) < 2 {
		return 0, false
	}
	rss, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return rss, true
}

func serviceIndex(comm string) int {
	for index, svc := range services {
		if svc.prefix {
			if strings.HasPrefix(comm, svc.comm) {
				return index
			}
		} else if comm == svc.comm {
			return index
		}
	}
	return -1
}

// Sample scans the proc root and returns per-service usage in fixed order.
// The first call reports zero CPU% for every service.
func (s *Sampler) Sample() []Proc {
	return s.sampleAt(time.Now())
}

func (s *Sampler) sampleAt(now time.Time) []Proc {
	result := make([]Proc, len(services))
	for index, svc := range services {
		result[index].Name = svc.name
	}

	elapsed := now.Sub(s.prevTime).Seconds()
	first := s.prevTime.IsZero()
	next := make(map[int]uint64)
	var rssPages = make([]uint64, len(services))

	entries, err := os.ReadDir(s.procRoot)
	if err != nil {
		s.prev = next
		s.prevTime = now
		return result
	}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		dir := filepath.Join(s.procRoot, entry.Name())
		comm, err := os.ReadFile(filepath.Join(dir, "comm"))
		if err != nil {
			continue // vanished mid-scan
		}
		index := serviceIndex(strings.TrimSpace(string(comm)))
		if index < 0 {
			continue
		}
		if stat, err := os.ReadFile(filepath.Join(dir, "stat")); err == nil {
			if ticks, ok := parseStat(string(stat)); ok {
				next[pid] = ticks
				if prev, seen := s.prev[pid]; seen && !first && elapsed > 0 && ticks >= prev {
					result[index].CPUPct += float64(ticks-prev) / (elapsed * s.hertz) * 100
				}
			}
		}
		if statm, err := os.ReadFile(filepath.Join(dir, "statm")); err == nil {
			if pages, ok := parseStatm(string(statm)); ok {
				rssPages[index] += pages
			}
		}
	}

	for index := range result {
		if result[index].CPUPct < 0 {
			result[index].CPUPct = 0
		}
		result[index].MemMB = int(rssPages[index] * uint64(s.pageSize) / (1024 * 1024))
	}
	s.prev = next
	s.prevTime = now
	return result
}
