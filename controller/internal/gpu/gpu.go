// Package gpu provides best-effort, multi-vendor GPU observability.
package gpu

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const probeTimeout = 3 * time.Second

// KV is an ordered display parameter.
type KV struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Info describes the first GPU visible to the controller.
type Info struct {
	Present bool   `json:"present"`
	Vendor  string `json:"vendor"`
	Model   string `json:"model"`
	Params  []KV   `json:"params"`
	Sampler string `json:"sampler"`

	devicePath string
}

// Usage is one utilization and memory sample.
type Usage struct {
	UtilPct    float64
	MemUsedMB  float64
	MemTotalMB float64
}

type runner interface {
	LookPath(string) (string, error)
	Run(context.Context, string, ...string) ([]byte, error)
}

type processRunner struct{}

func (processRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }

func (processRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

func run(r runner, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	return r.Run(ctx, name, args...)
}

// Detect probes GPU vendors in priority order and never reports probe failures.
func Detect() Info {
	return detect(processRunner{}, "/sys")
}

func detect(r runner, sysRoot string) Info {
	if info, ok := detectNVIDIA(r); ok {
		return info
	}
	if info, ok := detectAMD(r, sysRoot); ok {
		return info
	}
	if info, ok := detectIntel(sysRoot); ok {
		return info
	}
	return Info{}
}

func csvRows(output []byte) ([][]string, error) {
	rows, err := csv.NewReader(strings.NewReader(strings.TrimSpace(string(output)))).ReadAll()
	if err != nil {
		return nil, err
	}
	for i := range rows {
		for j := range rows[i] {
			rows[i][j] = strings.TrimSpace(rows[i][j])
		}
	}
	return rows, nil
}

func number(value string) (float64, error) {
	return strconv.ParseFloat(strings.TrimSpace(value), 64)
}

func displayNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func detectNVIDIA(r runner) (Info, bool) {
	output, err := run(r, "nvidia-smi",
		"--query-gpu=name,memory.total,driver_version,utilization.gpu,memory.used",
		"--format=csv,noheader,nounits")
	if err != nil {
		return Info{}, false
	}
	rows, err := csvRows(output)
	if err != nil || len(rows) == 0 || len(rows[0]) < 5 || rows[0][0] == "" {
		return Info{}, false
	}
	total, err := number(rows[0][1])
	if err != nil {
		return Info{}, false
	}
	model := rows[0][0]
	if len(rows) > 1 {
		model += fmt.Sprintf(" +%d more", len(rows)-1)
	}
	info := Info{
		Present: true, Vendor: "nvidia", Model: model, Sampler: "nvidia-smi",
		Params: []KV{{Key: "VRAM", Value: displayNumber(total) + " MiB"}, {Key: "Driver", Value: rows[0][2]}},
	}
	if cuda, cudaErr := run(r, "nvidia-smi", "--query-gpu=cuda_version", "--format=csv,noheader,nounits"); cudaErr == nil {
		if cudaRows, parseErr := csvRows(cuda); parseErr == nil && len(cudaRows) > 0 &&
			len(cudaRows[0]) > 0 && cudaRows[0][0] != "" {
			info.Params = append(info.Params, KV{Key: "CUDA", Value: cudaRows[0][0]})
		}
	}
	return info, true
}

func drmDevices(sysRoot string) []string {
	paths, _ := filepath.Glob(filepath.Join(sysRoot, "class", "drm", "card*", "device", "vendor"))
	return paths
}

func readTrimmed(path string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(content))
}

func pciDevicePath(vendorPath string) string {
	return filepath.Dir(vendorPath)
}

func pciID(devicePath string) string {
	return strings.TrimPrefix(strings.ToLower(readTrimmed(filepath.Join(devicePath, "device"))), "0x")
}

func amdSysfsInfo(sysRoot string) (Info, bool) {
	for _, vendorPath := range drmDevices(sysRoot) {
		if strings.ToLower(readTrimmed(vendorPath)) != "0x1002" {
			continue
		}
		device := pciDevicePath(vendorPath)
		model := readTrimmed(filepath.Join(device, "product_name"))
		if model == "" {
			id := pciID(device)
			if id == "" {
				id = "unknown"
			}
			model = "AMD GPU (1002:" + id + ")"
		}
		info := Info{Present: true, Vendor: "amd", Model: model, devicePath: device}
		if total, err := number(readTrimmed(filepath.Join(device, "mem_info_vram_total"))); err == nil {
			info.Params = append(info.Params, KV{Key: "VRAM", Value: displayNumber(total/(1024*1024)) + " MiB"})
		}
		if readTrimmed(filepath.Join(device, "gpu_busy_percent")) != "" {
			info.Sampler = "amd-sysfs"
		}
		return info, true
	}
	return Info{}, false
}

func flattenJSON(value any, values map[string]string) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			switch scalar := child.(type) {
			case string:
				values[strings.ToLower(key)] = scalar
			case float64:
				values[strings.ToLower(key)] = displayNumber(scalar)
			default:
				flattenJSON(child, values)
			}
		}
	case []any:
		for _, child := range typed {
			flattenJSON(child, values)
		}
	}
}

func firstValue(values map[string]string, fragments ...string) string {
	for _, fragment := range fragments {
		for key, value := range values {
			if strings.Contains(key, fragment) && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}

func detectROCm(r runner) (Info, bool) {
	if _, err := r.LookPath("rocm-smi"); err != nil {
		return Info{}, false
	}
	output, err := run(r, "rocm-smi", "--showproductname", "--showmeminfo", "vram", "--json")
	if err != nil {
		return Info{}, false
	}
	var decoded any
	if json.Unmarshal(output, &decoded) != nil {
		return Info{}, false
	}
	values := make(map[string]string)
	flattenJSON(decoded, values)
	model := firstValue(values, "card series", "product name", "card model", "card sku")
	if model == "" {
		return Info{}, false
	}
	info := Info{Present: true, Vendor: "amd", Model: model}
	if raw := firstValue(values, "vram total"); raw != "" {
		if bytes, parseErr := number(raw); parseErr == nil {
			info.Params = append(info.Params, KV{Key: "VRAM", Value: displayNumber(bytes/(1024*1024)) + " MiB"})
		}
	}
	return info, true
}

func detectAMD(r runner, sysRoot string) (Info, bool) {
	if _, err := r.LookPath("rocm-smi"); err == nil {
		if info, ok := detectROCm(r); ok {
			return info, true
		}
	}
	return amdSysfsInfo(sysRoot)
}

func detectIntel(sysRoot string) (Info, bool) {
	for _, vendorPath := range drmDevices(sysRoot) {
		if strings.ToLower(readTrimmed(vendorPath)) != "0x8086" {
			continue
		}
		device := pciDevicePath(vendorPath)
		model := readTrimmed(filepath.Join(device, "label"))
		if model == "" {
			id := pciID(device)
			if id == "" {
				id = "unknown"
			}
			model = "Intel GPU (8086:" + id + ")"
		}
		return Info{Present: true, Vendor: "intel", Model: model, devicePath: device}, true
	}
	return Info{}, false
}

// Sample reads one utilization point when the detected GPU has a sampler.
func Sample(info Info) (Usage, bool) {
	return sample(processRunner{}, info)
}

func sample(r runner, info Info) (Usage, bool) {
	switch info.Sampler {
	case "nvidia-smi":
		output, err := run(r, "nvidia-smi",
			"--query-gpu=name,memory.total,driver_version,utilization.gpu,memory.used",
			"--format=csv,noheader,nounits")
		rows, parseErr := csvRows(output)
		if err != nil || parseErr != nil || len(rows) == 0 || len(rows[0]) < 5 {
			return Usage{}, false
		}
		util, utilErr := number(rows[0][3])
		used, usedErr := number(rows[0][4])
		total, totalErr := number(rows[0][1])
		if utilErr != nil || usedErr != nil || totalErr != nil {
			return Usage{}, false
		}
		return Usage{UtilPct: util, MemUsedMB: used, MemTotalMB: total}, true
	case "amd-sysfs":
		util, utilErr := number(readTrimmed(filepath.Join(info.devicePath, "gpu_busy_percent")))
		used, usedErr := number(readTrimmed(filepath.Join(info.devicePath, "mem_info_vram_used")))
		total, totalErr := number(readTrimmed(filepath.Join(info.devicePath, "mem_info_vram_total")))
		if utilErr != nil {
			return Usage{}, false
		}
		usage := Usage{UtilPct: util}
		if usedErr == nil && totalErr == nil {
			usage.MemUsedMB = used / (1024 * 1024)
			usage.MemTotalMB = total / (1024 * 1024)
		}
		return usage, true
	default:
		return Usage{}, false
	}
}
