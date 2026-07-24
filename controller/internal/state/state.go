// Package state collects and broadcasts controller state snapshots.
package state

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mayanklahiri/virtualme/controller/internal/gpu"
	"github.com/mayanklahiri/virtualme/controller/internal/health"
	"github.com/mayanklahiri/virtualme/controller/internal/jobs"
	"github.com/mayanklahiri/virtualme/controller/internal/metrics"
	"github.com/mayanklahiri/virtualme/controller/internal/procstat"
)

// System contains lightweight host resource measurements.
type System struct {
	Load1         float64 `json:"load1"`
	MemUsedMB     int     `json:"memUsedMB"`
	MemTotalMB    int     `json:"memTotalMB"`
	DiskFreeMB    int     `json:"diskFreeMB"`
	DiskTotalMB   int     `json:"diskTotalMB"`
	GPUUtil       float64 `json:"gpuUtil,omitempty"`
	GPUMemMB      float64 `json:"gpuMemMB,omitempty"`
	GPUMemTotalMB float64 `json:"gpuMemTotalMB,omitempty"`
}

// Scheduler describes the server-local scheduling clock.
type Scheduler struct {
	LocalTime string   `json:"localTime"`
	TZ        string   `json:"tz"`
	Active    []string `json:"active"`
}

// Jiggler describes the persisted ambient-motion setting.
type Jiggler struct {
	Enabled bool `json:"enabled"`
}

// Network identifies the controller listener and its non-local interfaces.
type Network struct {
	Port  int      `json:"port"`
	Addrs []string `json:"addrs"`
}

// Runtime contains build and listener facts supplied by the controller.
type Runtime struct {
	Version  string
	HTTPAddr string
}

// Snapshot is the websocket state message consumed by the SPA.
type Snapshot struct {
	Type      string           `json:"type"`
	Ts        int64            `json:"ts"`
	UptimeSec int64            `json:"uptimeSec"`
	Hostname  string           `json:"hostname"`
	Version   string           `json:"version"`
	Net       Network          `json:"net"`
	OK        bool             `json:"ok"`
	Services  []health.Service `json:"services"`
	System    System           `json:"system"`
	Processes []procstat.Proc  `json:"processes"`
	Cores     []float64        `json:"cores"`
	Scheduler Scheduler        `json:"scheduler"`
	Jiggler   Jiggler          `json:"jiggler"`
	GPU       gpu.Info         `json:"gpu"`
}

func schedulerState(now time.Time) Scheduler {
	zone := os.Getenv("TZ")
	if zone == "" {
		zone = now.Location().String()
	}
	if zone == "" || zone == "Local" {
		_, offset := now.Zone()
		sign := "+"
		if offset < 0 {
			sign = "-"
			offset = -offset
		}
		zone = "UTC" + sign + fmt.Sprintf("%02d:%02d", offset/3600, offset%3600/60)
	}
	return Scheduler{
		LocalTime: now.Format(time.RFC3339), TZ: zone, Active: jobs.ActiveBuckets(now),
	}
}

// Collector periodically gathers, records, and broadcasts snapshots.
type Collector struct {
	cfg       health.Config
	sampler   *procstat.Sampler
	store     *metrics.Store
	procRoot  string
	broadcast func([]byte)
	started   time.Time
	period    time.Duration
	hostname  string
	version   string
	network   Network
	dataDir   string
	jiggler   func() bool
	gpu       gpu.Info
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

// ReadDisk returns available and total filesystem capacity in MiB.
func ReadDisk(path string) (freeMB, totalMB int) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0
	}
	return int(stat.Bavail * uint64(stat.Bsize) / (1024 * 1024)),
		int(stat.Blocks * uint64(stat.Bsize) / (1024 * 1024))
}

// NewCollector creates a two-second state collector sampling procRoot.
func NewCollector(cfg health.Config, procRoot string, store *metrics.Store, broadcast func([]byte), gpuInfo gpu.Info, jiggler func() bool, runtime ...Runtime) *Collector {
	hostname, _ := os.Hostname()
	dataDir := os.Getenv("VM_DATA_DIR")
	if dataDir == "" {
		dataDir = "/home/virtualme/.virtualme"
	}
	buildVersion := "dev"
	httpAddr := ":8080"
	if len(runtime) > 0 {
		if runtime[0].Version != "" {
			buildVersion = runtime[0].Version
		}
		if runtime[0].HTTPAddr != "" {
			httpAddr = runtime[0].HTTPAddr
		}
	}
	collector := &Collector{
		cfg:       cfg,
		sampler:   procstat.NewSampler(procRoot),
		store:     store,
		procRoot:  procRoot,
		broadcast: broadcast,
		started:   time.Now(),
		period:    2 * time.Second,
		hostname:  hostname,
		version:   buildVersion,
		network:   discoverNetwork(httpAddr),
		dataDir:   dataDir,
		jiggler:   jiggler,
		gpu:       gpuInfo,
	}
	return collector
}

func listenerPort(addr string) int {
	_, rawPort, err := net.SplitHostPort(addr)
	if err != nil {
		return 8080
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return 8080
	}
	return port
}

func usableAddrs(addrs []net.Addr) []string {
	ipv4 := make([]string, 0, 2)
	ipv6 := make([]string, 0, 2)
	seen := make(map[string]bool)
	for _, addr := range addrs {
		ip, _, err := net.ParseCIDR(addr.String())
		if err != nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			continue
		}
		value := ip.String()
		if seen[value] {
			continue
		}
		seen[value] = true
		if ip.To4() != nil {
			ipv4 = append(ipv4, value)
		} else {
			ipv6 = append(ipv6, value)
		}
	}
	result := append(ipv4, ipv6...)
	if len(result) > 2 {
		result = result[:2]
	}
	return result
}

func discoverNetwork(httpAddr string) Network {
	var addrs []net.Addr
	ifaces, err := net.Interfaces()
	if err == nil {
		for _, iface := range ifaces {
			if ifaceAddrs, addrErr := iface.Addrs(); addrErr == nil {
				addrs = append(addrs, ifaceAddrs...)
			}
		}
	}
	return Network{Port: listenerPort(httpAddr), Addrs: usableAddrs(addrs)}
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
	system := ReadSystem(
		readFile(filepath.Join(c.procRoot, "loadavg")),
		readFile(filepath.Join(c.procRoot, "meminfo")),
	)
	system.DiskFreeMB, system.DiskTotalMB = ReadDisk(c.dataDir)
	if c.gpu.Sampler != "" {
		if usage, ok := gpu.Sample(c.gpu); ok {
			system.GPUUtil = usage.UtilPct
			system.GPUMemMB = usage.MemUsedMB
			system.GPUMemTotalMB = usage.MemTotalMB
		}
	}
	processes := c.sampler.Sample()
	cores := c.sampler.Cores()
	now := time.Now()
	snapshot := Snapshot{
		Type:      "state",
		Ts:        now.UnixMilli(),
		UptimeSec: int64(time.Since(c.started) / time.Second),
		Hostname:  c.hostname,
		Version:   c.version,
		Net:       c.network,
		OK:        report.OK,
		Services:  report.Services,
		System:    system,
		Processes: processes,
		Cores:     cores,
		Scheduler: schedulerState(now),
		GPU:       c.gpu,
	}
	if c.jiggler != nil {
		snapshot.Jiggler.Enabled = c.jiggler()
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return
	}
	procMem := make([]int, len(processes))
	procCPU := make([]float64, len(processes))
	for i, process := range processes {
		procMem[i] = process.MemMB
		procCPU[i] = process.CPUPct
	}
	if c.store != nil {
		c.store.Add(metrics.Sample{
			Ts: snapshot.Ts, Cores: cores, ProcCPU: procCPU, ProcMemMB: procMem,
			Load1: system.Load1, MemUsedMB: system.MemUsedMB, MemTotalMB: system.MemTotalMB,
			GPUUtil: system.GPUUtil, GPUMemMB: system.GPUMemMB,
		})
	}
	c.broadcast(payload)
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
