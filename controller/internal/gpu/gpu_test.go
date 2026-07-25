package gpu

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type fakeResult struct {
	output string
	err    error
}

type fakeRunner struct {
	paths   map[string]bool
	results []fakeResult
	calls   [][]string
}

func (f *fakeRunner) LookPath(name string) (string, error) {
	if f.paths[name] {
		return "/usr/bin/" + name, nil
	}
	return "", errors.New("not found")
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if _, ok := ctx.Deadline(); !ok {
		return nil, errors.New("missing deadline")
	}
	f.calls = append(f.calls, append([]string{name}, args...))
	if len(f.results) == 0 {
		return nil, errors.New("unexpected call")
	}
	result := f.results[0]
	f.results = f.results[1:]
	return []byte(result.output), result.err
}

func TestDetectNVIDIAAndSample(t *testing.T) {
	runner := &fakeRunner{results: []fakeResult{
		{output: "RTX 4090, 24564, 560.35, 17, 1234\nRTX 4080, 16376, 560.35, 2, 20\n"},
		{output: "12.6\n"},
		{output: "RTX 4090, 24564, 560.35, 81, 8192\n"},
	}}
	info := detect(runner, t.TempDir())
	if !info.Present || info.Vendor != "nvidia" || info.Model != "RTX 4090 +1 more" ||
		info.Sampler != "nvidia-smi" {
		t.Fatalf("Detect NVIDIA = %+v", info)
	}
	// 24564 MiB rounds to 24 GB (binary GB, one decimal only when fractional).
	wantParams := []KV{{Key: "VRAM", Value: "24 GB"}, {Key: "Driver", Value: "560.35"}, {Key: "CUDA", Value: "12.6"}}
	if !reflect.DeepEqual(info.Params, wantParams) {
		t.Fatalf("params = %+v, want %+v", info.Params, wantParams)
	}
	usage, ok := sample(runner, info)
	if !ok || usage != (Usage{UtilPct: 81, MemUsedMB: 8192, MemTotalMB: 24564}) {
		t.Fatalf("Sample NVIDIA = (%+v, %v)", usage, ok)
	}
}

func writeSysfs(t *testing.T, root, card, name, value string) {
	t.Helper()
	path := filepath.Join(root, "class", "drm", card, "device", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectAMDSysfsAndSample(t *testing.T) {
	root := t.TempDir()
	writeSysfs(t, root, "card0", "vendor", "0x1002\n")
	writeSysfs(t, root, "card0", "device", "0x744c\n")
	writeSysfs(t, root, "card0", "product_name", "Radeon RX 7900 XTX\n")
	writeSysfs(t, root, "card0", "gpu_busy_percent", "73\n")
	writeSysfs(t, root, "card0", "mem_info_vram_used", "2147483648\n")
	writeSysfs(t, root, "card0", "mem_info_vram_total", "25769803776\n")
	runner := &fakeRunner{results: []fakeResult{{err: errors.New("no nvidia")}}}

	info := detect(runner, root)
	if !info.Present || info.Vendor != "amd" || info.Model != "Radeon RX 7900 XTX" ||
		info.Sampler != "amd-sysfs" {
		t.Fatalf("Detect AMD = %+v", info)
	}
	if !reflect.DeepEqual(info.Params, []KV{{Key: "VRAM", Value: "24 GB"}}) {
		t.Fatalf("params = %+v", info.Params)
	}
	usage, ok := sample(runner, info)
	if !ok || usage != (Usage{UtilPct: 73, MemUsedMB: 2048, MemTotalMB: 24576}) {
		t.Fatalf("Sample AMD = (%+v, %v)", usage, ok)
	}
}

func TestDetectAMDPresenceOnly(t *testing.T) {
	root := t.TempDir()
	writeSysfs(t, root, "card1", "vendor", "0x1002")
	writeSysfs(t, root, "card1", "device", "0x164e")
	runner := &fakeRunner{results: []fakeResult{{err: errors.New("no nvidia")}}}
	info := detect(runner, root)
	if info.Model != "AMD GPU (1002:164e)" || info.Sampler != "" {
		t.Fatalf("Detect AMD presence = %+v", info)
	}
}

func TestDetectROCm(t *testing.T) {
	runner := &fakeRunner{
		paths: map[string]bool{"rocm-smi": true},
		results: []fakeResult{
			{err: errors.New("no nvidia")},
			{output: `{"card0":{"Card series":"AMD Radeon PRO W7900","VRAM Total Memory (B)":50331648000}}`},
		},
	}
	info := detect(runner, t.TempDir())
	if !info.Present || info.Vendor != "amd" || info.Model != "AMD Radeon PRO W7900" ||
		info.Sampler != "" || !reflect.DeepEqual(info.Params, []KV{{Key: "VRAM", Value: "46.9 GB"}}) {
		t.Fatalf("Detect ROCm = %+v", info)
	}
}

func TestDetectIntelFromLabelAndPCI(t *testing.T) {
	for _, test := range []struct {
		name  string
		label string
		want  string
	}{
		{name: "label", label: "Intel Arc A770", want: "Intel Arc A770"},
		{name: "pci", want: "Intel GPU (8086:56a0)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeSysfs(t, root, "card2", "vendor", "0x8086")
			writeSysfs(t, root, "card2", "device", "0x56a0")
			if test.label != "" {
				writeSysfs(t, root, "card2", "label", test.label)
			}
			runner := &fakeRunner{results: []fakeResult{{err: errors.New("no nvidia")}}}
			info := detect(runner, root)
			if !info.Present || info.Vendor != "intel" || info.Model != test.want || info.Sampler != "" {
				t.Fatalf("Detect Intel = %+v", info)
			}
		})
	}
}

func TestDetectNoGPU(t *testing.T) {
	runner := &fakeRunner{results: []fakeResult{{err: errors.New("no nvidia")}}}
	if got := detect(runner, t.TempDir()); !reflect.DeepEqual(got, Info{}) {
		t.Fatalf("Detect no GPU = %+v", got)
	}
}

func TestProbeHasThreeSecondDeadline(t *testing.T) {
	runner := &fakeRunner{results: []fakeResult{{err: errors.New("no nvidia")}}}
	start := time.Now()
	_ = detect(runner, t.TempDir())
	if len(runner.calls) != 1 || time.Since(start) > time.Second {
		t.Fatalf("probe calls = %v", runner.calls)
	}
}
