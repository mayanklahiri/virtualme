# Spec 018: GPU Observability — Presence Widget and Usage Series

| | |
|---|---|
| Status | Executed (2026-07-23) |
| Depends on | `specs/019-chart-overhaul.md` (chart component: titles, ticks, lookback — **execute 019 first** despite the lower number here), `specs/005-console-ui.md` (status widgets), `specs/007-persistence-locality.md` (metrics tiers) |
| Produces | Multi-vendor best-effort GPU detection (`controller/internal/gpu`); a Status-page GPU widget (presence, vendor, model, extractable params); a Home-page GPU line when present; a GPU utilization time-series chart when utilization is samplable; metrics-tier extension |
| Followed by | Future specs (GPU llama build remains out of scope, per spec 008) |

## 0. Executor instructions

- Constitution binds: Go stdlib only; vendor tools are probed with `exec` and their absence is a normal, silent result — never an error, never a health failure.
- The container only sees a GPU when started with `--gpus` (NVIDIA container toolkit injects `nvidia-smi` and `/dev/nvidia*`) or with explicit `--device` passthrough (AMD/Intel `/dev/dri`). "No GPU" is the common case and must render cleanly.
- Stop-on-red; finish with §7 Acceptance.

## 1. Detection — `controller/internal/gpu`

`func Detect() Info` — run once at controller startup (cache the result; hardware does not hot-plug in this deployment) plus `func Sample(info Info) (Usage, bool)` — called by the metrics collector when `info.Present`.

```go
type Info struct {
    Present bool     `json:"present"`
    Vendor  string   `json:"vendor"`  // "nvidia" | "amd" | "intel" | ""
    Model   string   `json:"model"`
    Params  []KV     `json:"params"`  // ordered extractable params, e.g. {"VRAM","8 GB"},{"Driver","560.35"},{"CUDA","12.6"} (VRAM in binary GB, one decimal when fractional)
    Sampler string   `json:"sampler"` // "nvidia-smi" | "amd-sysfs" | "" (empty = presence only, no usage series)
}
type Usage struct { UtilPct float64; MemUsedMB, MemTotalMB float64 }
```

Probe order (first hit wins; each probe has a 3 s exec timeout):

1. **NVIDIA**: `nvidia-smi --query-gpu=name,memory.total,driver_version,utilization.gpu,memory.used --format=csv,noheader,nounits`. Exec error or non-zero exit ⇒ not NVIDIA. Params: VRAM, Driver; add CUDA version from `nvidia-smi --query-gpu=cuda_version ...` if the field is supported (ignore failure). `Sampler:"nvidia-smi"`; `Sample` re-runs the utilization/memory query.
2. **AMD**: if `rocm-smi` exists (`exec.LookPath`), use `rocm-smi --showproductname --showmeminfo vram --json` (parse defensively). Else sysfs: any `/sys/class/drm/card*/device/vendor` containing `0x1002` ⇒ present; Model from `/sys/class/drm/cardN/device/product_name` if readable, else PCI id; utilization from `/sys/class/drm/cardN/device/gpu_busy_percent` and VRAM from `mem_info_vram_used`/`mem_info_vram_total` when readable ⇒ `Sampler:"amd-sysfs"`, otherwise presence-only.
3. **Intel**: `/sys/class/drm/card*/device/vendor` containing `0x8086` ⇒ present, Vendor `intel`, Model from `lspci`-free sources only (sysfs `device/label` or PCI device id rendered as `Intel GPU (8086:XXXX)`). `intel_gpu_top` is not in the image and root-only — presence-only (`Sampler:""`).
4. Nothing found ⇒ `Info{Present:false}`.

Hermetic tests: fake `Runner` for the CLI probes; `t.TempDir()`-rooted fake sysfs trees for AMD/Intel paths (make the sysfs root injectable, default `/sys`).

## 2. Metrics integration

- `controller/internal/state/state.go`: add `"gpu": Info` (the cached struct) to every snapshot — cheap, static. When `Sampler != ""`, the 2 s collector also calls `Sample` and adds `"gpuUtil": …, "gpuMemMB": …` to the snapshot's system section.
- `controller/internal/metrics/metrics.go`: extend the tier sample struct with optional `gpuUtil float64` and `gpuMemMB float64` (omit from JSON when GPU absent — `omitempty`); persisted tiers stay backward-compatible (old files simply lack the fields; loading must tolerate that).
- No health probe changes: GPU absence is not unhealthy.

## 3. Status page widget + chart

1. **Widget**, placed in the `.system-grid` after the load/memory meters:

```html
<article class="metric gpu-card">
  <div><label>GPU <strong id="gpu-name">none detected</strong></label>
  <dl id="gpu-params" class="gpu-params"></dl></div>
</article>
```

`render.js`: absent ⇒ name text `none detected` (muted) and the params list hidden, plus a one-line muted caption `Start with --gpus to pass one through.` Present ⇒ `<Vendor> <Model>` and one `<div><dt>k</dt><dd>v</dd></div>` per param, monospace values.

2. **Chart** (only when `state.gpu.sampler` non-empty): a third chart figure after Memory, built with the spec 019 component:
   - Title (economist-style, per 019): **GPU utilization** with subtitle `percent busy · memory MiB`.
   - Two series: utilization % (left axis 0–100) drawn as the standard area/bars in `--p1`; memory used MiB as a line overlay in `--p2` scaled to its own max = `MemTotalMB` (dual scale is acceptable here because the memory line is contextual; label the right side max with a single tick text `«total» MiB`).
   - Hidden entirely (`figure hidden`) when no sampler; decided once per page load from the first `state` frame.
3. **Home page**: in `.home-facts`, after Disk: `<span id="home-gpu-row" hidden>GPU <strong id="home-gpu">—</strong></span>`; `render.js` unhides with `Vendor Model` when present. (Spec 024 restyles this row group; coordinate — this spec only adds the data hook.)

## 4. CLI and docs

- `src/commands/help.js`: extend the `--gpus` line: passing a GPU also lights up the GPU status widget and usage chart. No new flags.
- Docs step: `/master-update` — operate skill (GPU widget semantics; `--gpus all` for NVIDIA, `--device /dev/dri` style passthrough for AMD/Intel is host-specific and out of CLI scope), develop skill (`internal/gpu` row, injectable sysfs root note), README.

## 5. Tests

- Hermetic per §1; metrics round-trip test with GPU fields present and absent (old tier file loads).
- e2e: unchanged by default. Add an env-gated probe `E2E_GPU=1` asserting the snapshot carries `gpu.present:true` — for use on GPU hosts only.
- Manual verification matrix (record results in the PR/commit message, not CI): no-GPU host (widget shows none), NVIDIA host with `--gpus all` (widget + chart live).

## 6. Non-goals

- Wiring `VM_LLAMA_GPU` to an actual GPU llama build (future spec; the env plumbing from spec 008 stays dormant).
- Per-process GPU accounting, MIG, multi-GPU (only the first GPU is reported; note `+N more` in Model when >1 NVIDIA GPU rows return).

## 7. Acceptance checklist

- [x] `npm run check` green; zero new deps.
- [x] On a host without GPUs: widget renders `none detected`, no GPU chart, snapshots carry `gpu.present:false`, nothing logs errors.
- [ ] On an NVIDIA host with `--gpus all`: widget shows model/VRAM/driver; GPU chart draws with 019 ticks/titles/lookbacks; utilization responds to load (`docker exec … llama-bench` or any GPU burn).
- [x] AMD sysfs fake tests prove presence + busy-percent sampling without rocm-smi.
- [x] Old persisted metrics tiers load cleanly after the schema extension.

## Amendments

### 2026-07-23 — Direct execution before spec 019

At operator direction, spec 018 was executed directly after spec 017 without
executing the later-numbered spec 019. The GPU chart uses the existing shared
lookback state and chart primitives, with the required title, utilization
bars, memory overlay, dual scale, theme tokens, and first-state visibility.
Spec 019 remains draft and will later refactor all three charts and add its
boundary-aligned x-axis ticks; none of its broader CPU/memory chart overhaul
was implemented here.

The NVIDIA-host acceptance item remains unchecked because this execution
environment has no passed-through GPU. `E2E_GPU=1 bash test/e2e.sh` starts with
`--gpus all` and performs that hardware-gated assertion.

### 2026-07-23 — Spec 019 chart integration completed

Spec 019 subsequently refactored the CPU, memory, and GPU charts onto the
shared chart component. The GPU chart now receives the common synchronized
lookback controls, boundary-aligned ticks, timestamp hover selection, and
`--p1`/`--p2` legend behavior while retaining its utilization bars, memory
overlay, dual scale, and hardware-gated visibility.

### 2026-07-24 — Default GPU passthrough and a Vulkan llama.cpp runtime

Investigation result: on a host with an RTX 3xxx-class card the console
showed no GPU because (a) the CLI only passed `--gpus` when explicitly
given, so no NVIDIA device was injected into the container, and (b) the
image ships a CPU-only llama.cpp, so even with passthrough the model would
not use the GPU. Both are fixed here.

1. **CLI auto-passthrough** (`src/commands/start.js`, `src/docker.js`).
   New `hostNvidia()` probe: a working `nvidia-smi -L` on PATH, or
   `docker info --format '{{json .Runtimes}}'` listing an `nvidia` runtime
   (NVIDIA Container Toolkit). When it fires and neither `--gpus` nor the
   new `--no-gpu` flag is given, `start` defaults to
   `--gpus all -e VM_LLAMA_GPU=1 -e NVIDIA_DRIVER_CAPABILITIES=all`.
   An explicit `--gpus <spec>` still wins (and now also sets both env
   vars); `--no-gpu` opts out entirely; `--gpus` plus `--no-gpu` is a
   usage error (exit 2). `NVIDIA_DRIVER_CAPABILITIES=all` makes the
   toolkit mount the NVIDIA Vulkan ICD, which `graphics`-less defaults
   omit. Unit tests cover the explicit, auto-detected, and opted-out
   paths. If Docker lacks the NVIDIA runtime despite detection, `docker
   run` fails loudly rather than silently degrading — acceptable for the
   trusted-host prototype; rerun with `--no-gpu`.
2. **GPU llama runtime — Vulkan, a recorded deviation from the "CUDA
   build" choice.** llama.cpp publishes no Linux CUDA prebuilts (verified
   against release b10091: CUDA binaries exist only for Windows; Linux
   offers CPU/Vulkan/ROCm/SYCL), and a from-source CUDA build would add a
   multi-gigabyte pinned CUDA toolchain layer. The Vulkan prebuilt runs on
   NVIDIA through the driver's Vulkan ICD with near-CUDA throughput for
   single-GPU inference, and also covers AMD/Intel GPUs. New append-only
   layer `docker/layers/018-llama-vulkan.sh` installs pinned
   `llama-b10091-bin-ubuntu-vulkan-x64.tar.gz` (sha256
   `8636767e0fdf440247913e4ba46a33fe02b8f13181bb11756ab890d73fdecdb4`)
   plus `libvulkan1` and `libegl1` into `/opt/llama-vulkan`; it exits 0
   with a notice on non-x86_64 (no upstream Vulkan prebuilt), leaving
   those hosts CPU-only. The existing CPU layer 002 is untouched.
   `libegl1` was added after a live diagnosis (2026-07-24, RTX 3060):
   without GLVND's `libEGL.so.1` the NVIDIA Vulkan ICD's
   `vk_icdGetInstanceProcAddr` fails to initialize (loader error "Could
   not get 'vkCreateInstance'"), llama.cpp enumerates zero devices, and
   inference silently falls back to the CPU at 100% on all cores while
   the GPU idles.
3. **Runtime selection** (`svc-llama/run`). At service start: if
   `VM_LLAMA_GPU=1`, `/dev/nvidiactl` exists (a device was actually
   injected), and `/opt/llama-vulkan/llama-server` is present, exec the
   Vulkan build with `--n-gpu-layers 999` (full offload); otherwise exec
   the CPU build exactly as before. The chosen runtime is echoed to the
   service log (`svc-llama: runtime …`) for smoke/soak grepping, and when
   the GPU path is taken the script also logs `llama-server
   --list-devices` output so an empty device list (ICD init failure,
   silent CPU fallback) is visible in the logs. A passthrough failure
   therefore degrades to the CPU path, never a crash loop.

### 2026-07-24 — GPU chart split (spec 026 S3)

The combined utilization + memory dual-scale chart (§3.2) is superseded by
two default-renderer charts, "GPU utilization" (percent, 0–100) and "GPU
memory" (displayed in GB, 0–memTotal), side by side on desktop. Same
series data, same first-sampler visibility gate.
