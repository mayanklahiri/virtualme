import { mkdirSync } from "node:fs";
import { homedir } from "node:os";
import { join, resolve } from "node:path";
import { parseArgs } from "node:util";
import { CONTAINER, DATA_MOUNT, IMAGE, PORT, TAG } from "../config.js";
import { containerState, daemonUp, haveDocker, hostNvidia, run as runDocker } from "../docker.js";
import { green, red } from "../ansi.js";
import { run as buildRun } from "./build.js";

const USAGE = "start [--data <dir>] [--no-browser-sandbox] [--gpus <spec>] [--no-gpu] [--rebuild]";

/**
 * Resolve the host data directory: --data flag > VIRTUALME_DATA > ~/.virtualme.
 * @param {string | undefined} flag
 */
function resolveDataDir(flag) {
  return resolve(flag ?? process.env.VIRTUALME_DATA ?? join(homedir(), ".virtualme"));
}

/**
 * Stop and remove the container if present (idempotent).
 * @param {(args: string[]) => number} docker
 * @param {{ containerState: () => string }} probes
 */
function stopContainer(docker, probes) {
  if (probes.containerState() === "absent") return 0;
  const stopCode = docker(["stop", CONTAINER]);
  if (stopCode !== 0) return stopCode;
  return docker(["rm", CONTAINER]);
}

/**
 * @param {string[]} argv
 * @param {(args: string[]) => number} [docker]
 * @param {{ haveDocker: () => boolean, daemonUp: () => boolean, containerState: () => string, nvidiaGPU?: () => boolean }} [probes]
 */
export function run(argv, docker = runDocker, probes = { haveDocker, daemonUp, containerState, nvidiaGPU: hostNvidia }) {
  /** @type {{ data?: string, "no-browser-sandbox"?: boolean, gpus?: string, "no-gpu"?: boolean, rebuild?: boolean }} */
  let flags;
  try {
    flags = parseArgs({
      args: argv,
      options: {
        data: { type: "string" },
        "no-browser-sandbox": { type: "boolean" },
        gpus: { type: "string" },
        "no-gpu": { type: "boolean" },
        rebuild: { type: "boolean" },
      },
    }).values;
    if (flags.gpus && flags["no-gpu"]) throw new Error("--gpus conflicts with --no-gpu");
  } catch {
    console.error(red(`error: usage: ${USAGE}`));
    return 2;
  }
  if (!probes.haveDocker()) {
    console.error(red("error: docker is not installed or not on PATH"));
    return 1;
  }
  if (!probes.daemonUp()) {
    console.error(red("error: docker daemon is not reachable"));
    return 1;
  }
  if (flags.rebuild) {
    const buildCode = buildRun([], docker);
    if (buildCode !== 0) return buildCode;
    const stopCode = stopContainer(docker, probes);
    if (stopCode !== 0) return stopCode;
  }
  const state = probes.containerState();
  if (state === "running") {
    console.error(red("error: virtualme is already running"));
    return 1;
  }
  if (state === "exited" && docker(["rm", CONTAINER]) !== 0) return 1;

  const dataDir = resolveDataDir(flags.data);
  mkdirSync(dataDir, { recursive: true });
  const uid = process.getuid?.() ?? 1000;
  const gid = process.getgid?.() ?? 1000;
  const timezone = process.env.TZ || Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  const dockerArgs = [
    "run", "-d", "--name", CONTAINER, "--restart", "unless-stopped",
    "--shm-size=1g",
    "--user", `${uid}:${gid}`,
    "--tmpfs", `/run:exec,mode=755,uid=${uid},gid=${gid}`,
    "--tmpfs", "/tmp:mode=1777",
    "-p", `${PORT}:${PORT}`,
    "-v", `${dataDir}:${DATA_MOUNT}`,
    "-e", `TZ=${timezone}`,
  ];
  if (flags["no-browser-sandbox"]) {
    dockerArgs.push("-e", "VM_CHROMIUM_NO_SANDBOX=1");
  }
  // GPU passthrough: explicit --gpus wins, --no-gpu opts out, otherwise a
  // detected host NVIDIA stack defaults to full passthrough. The capability
  // env exposes the NVIDIA Vulkan ICD for the GPU llama runtime (spec 018).
  const gpuSpec = flags["no-gpu"] ? "" : flags.gpus ?? (probes.nvidiaGPU?.() ? "all" : "");
  if (gpuSpec) {
    dockerArgs.push("--gpus", gpuSpec,
      "-e", "VM_LLAMA_GPU=1", "-e", "NVIDIA_DRIVER_CAPABILITIES=all");
  }
  if (process.env.VM_MAIL_SMARTHOST) {
    dockerArgs.push("--add-host", "vmhost:host-gateway");
  }
  for (const name of [
    "VM_MAIL_MAILNAME", "VM_MAIL_FROM", "VM_MAIL_SMARTHOST",
    "VM_MAIL_SMARTHOST_PORT", "VM_MAIL_SMARTHOST_USER",
    "VM_MAIL_SMARTHOST_PASS", "VM_MAIL_DKIM_DOMAIN", "VM_MAIL_DKIM_SELECTOR",
    "VM_MAIL_FLUSH_SEC",
    "VM_TTS_CACHE_DIR", "VM_TTS_CACHE_MAX_MB",
  ]) {
    if (process.env[name] !== undefined) {
      dockerArgs.push("-e", `${name}=${process.env[name]}`);
    }
  }
  dockerArgs.push(`${IMAGE}:${TAG}`);
  const code = docker(dockerArgs);
  if (code === 0) {
    console.log(green(`Virtual Me is running at http://localhost:${PORT}`));
    console.log(`Data directory: ${dataDir}`);
    console.log("Follow startup with: virtualme logs -f");
  }
  return code;
}
