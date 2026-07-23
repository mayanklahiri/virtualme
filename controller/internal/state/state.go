// Package state collects and broadcasts controller state snapshots.
package state

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mayanklahiri/virtualme/controller/internal/health"
)

// System contains lightweight host resource measurements.
type System struct {
	Load1      float64 `json:"load1"`
	MemUsedMB  int     `json:"memUsedMB"`
	MemTotalMB int     `json:"memTotalMB"`
}

// Snapshot is the websocket state message consumed by the SPA.
type Snapshot struct {
	Type      string           `json:"type"`
	Ts        int64            `json:"ts"`
	UptimeSec int64            `json:"uptimeSec"`
	OK        bool             `json:"ok"`
	Services  []health.Service `json:"services"`
	System    System           `json:"system"`
}

// Collector periodically gathers and broadcasts snapshots.
type Collector struct {
	cfg       health.Config
	broadcast func([]byte)
	started   time.Time
	period    time.Duration
}

// ReadSystem parses Linux procfs load and memory data.
func ReadSystem(loadavg, meminfo string) System {
	var result System
	if fields := strings.Fields(loadavg); len(fields) > 0 {
		result.Load1, _ = strconv.ParseFloat(fields[0], 64)
	}
	var totalKB, availableKB int
	for line := range strings.SplitSeq(meminfo, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		switch strings.TrimSuffix(fields[0], ":") {
		case "MemTotal":
			totalKB = value
		case "MemAvailable":
			availableKB = value
		}
	}
	result.MemTotalMB = totalKB / 1024
	if totalKB >= availableKB {
		result.MemUsedMB = (totalKB - availableKB) / 1024
	}
	return result
}

// NewCollector creates a two-second state collector.
func NewCollector(cfg health.Config, broadcast func([]byte)) *Collector {
	return &Collector{
		cfg:       cfg,
		broadcast: broadcast,
		started:   time.Now(),
		period:    2 * time.Second,
	}
}

func readFile(path string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(content)
}

func (c *Collector) collect() {
	report := health.Gather(c.cfg)
	snapshot := Snapshot{
		Type:      "state",
		Ts:        time.Now().UnixMilli(),
		UptimeSec: int64(time.Since(c.started) / time.Second),
		OK:        report.OK,
		Services:  report.Services,
		System:    ReadSystem(readFile("/proc/loadavg"), readFile("/proc/meminfo")),
	}
	payload, err := json.Marshal(snapshot)
	if err == nil {
		c.broadcast(payload)
	}
}

// Run collects immediately and then every two seconds until cancellation.
func (c *Collector) Run(ctx context.Context) {
	c.collect()
	ticker := time.NewTicker(c.period)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.collect()
		}
	}
}
