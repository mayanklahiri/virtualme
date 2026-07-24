import test from "node:test";
import assert from "node:assert/strict";
import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import { run as build } from "../src/commands/build.js";
import { run as start } from "../src/commands/start.js";

const pkg = JSON.parse(readFileSync(new URL("../package.json", import.meta.url), "utf8"));

/** @param {string[]} args */
function cli(args) {
  return spawnSync(process.execPath, ["bin/virtualme.js", ...args], { encoding: "utf8" });
}

test("help exits successfully and contains Usage", () => {
  const result = cli(["help"]);
  assert.equal(result.status, 0);
  assert.match(result.stdout, /Usage/);
});

test("no arguments shows help", () => {
  const result = cli([]);
  assert.equal(result.status, 0);
  assert.match(result.stdout, /Usage/);
});

test("version prints package version", () => {
  const result = cli(["version"]);
  assert.equal(result.status, 0);
  assert.equal(result.stdout.trim(), pkg.version);
});

test("unknown command is a usage error", () => {
  const result = cli(["nope"]);
  assert.equal(result.status, 2);
  assert.match(result.stderr, /unknown command/);
});

test("build tags both the development and configured start image", () => {
  /** @type {string[] | undefined} */
  let invocation;
  const code = build([], (args) => {
    invocation = args;
    return 0;
  });
  assert.equal(code, 0);
  assert.deepEqual(invocation, [
    "build", "-f", "docker/Dockerfile",
    "-t", "mayanklahiri/virtualme:dev",
    "-t", "mayanklahiri/virtualme:latest",
    ".",
  ]);
});

test("start runs as the host user with the data dir mounted", () => {
  const parent = mkdtempSync(join(tmpdir(), "virtualme-test-"));
  const dataDir = join(parent, "data");
  try {
    assert.equal(existsSync(dataDir), false);
    /** @type {string[] | undefined} */
    let invocation;
    const probes = { haveDocker: () => true, daemonUp: () => true, containerState: () => "absent" };
    const code = start(["--data", dataDir], (args) => {
      invocation = args;
      return 0;
    }, probes);
    assert.equal(code, 0);
    assert.equal(existsSync(dataDir), true, "start must create the data dir");
    const uid = process.getuid?.() ?? 1000;
    const gid = process.getgid?.() ?? 1000;
    assert.deepEqual(invocation, [
      "run", "-d", "--name", "virtualme", "--restart", "unless-stopped",
      "--shm-size=1g",
      "--user", `${uid}:${gid}`,
      "--tmpfs", `/run:exec,mode=755,uid=${uid},gid=${gid}`,
      "--tmpfs", "/tmp:mode=1777",
      "-p", "8080:8080",
      "-v", `${dataDir}:/home/virtualme/.virtualme`,
      "-e", `TZ=${process.env.TZ || Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC"}`,
      "mayanklahiri/virtualme:latest",
    ]);
  } finally {
    rmSync(parent, { recursive: true, force: true });
  }
});

test("start forwards the host timezone", () => {
  const parent = mkdtempSync(join(tmpdir(), "virtualme-test-"));
  const previous = process.env.TZ;
  process.env.TZ = "Australia/Sydney";
  try {
    /** @type {string[] | undefined} */
    let invocation;
    const probes = { haveDocker: () => true, daemonUp: () => true, containerState: () => "absent" };
    assert.equal(start(["--data", join(parent, "data")], (args) => {
      invocation = args;
      return 0;
    }, probes), 0);
    assert.ok(invocation);
    assert.ok(invocation.includes("TZ=Australia/Sydney"));
  } finally {
    if (previous === undefined) delete process.env.TZ;
    else process.env.TZ = previous;
    rmSync(parent, { recursive: true, force: true });
  }
});

test("start can force Chromium's sandbox fallback without adding privileges", () => {
  const parent = mkdtempSync(join(tmpdir(), "virtualme-test-"));
  const dataDir = join(parent, "data");
  try {
    /** @type {string[] | undefined} */
    let invocation;
    const probes = { haveDocker: () => true, daemonUp: () => true, containerState: () => "absent" };
    const code = start(["--data", dataDir, "--no-browser-sandbox"], (args) => {
      invocation = args;
      return 0;
    }, probes);
    assert.equal(code, 0);
    assert.ok(invocation);
    assert.deepEqual(invocation.slice(-3), [
      "-e", "VM_CHROMIUM_NO_SANDBOX=1", "mayanklahiri/virtualme:latest",
    ]);
    assert.equal(invocation.includes("--cap-add"), false);
    assert.equal(invocation.includes("--security-opt"), false);
  } finally {
    rmSync(parent, { recursive: true, force: true });
  }
});

test("start forwards configured outbound-mail environment", () => {
  const parent = mkdtempSync(join(tmpdir(), "virtualme-test-"));
  const dataDir = join(parent, "data");
  const previous = process.env.VM_MAIL_SMARTHOST;
  const previousFlush = process.env.VM_MAIL_FLUSH_SEC;
  process.env.VM_MAIL_SMARTHOST = "relay.example";
  process.env.VM_MAIL_FLUSH_SEC = "15";
  try {
    /** @type {string[] | undefined} */
    let invocation;
    const probes = { haveDocker: () => true, daemonUp: () => true, containerState: () => "absent" };
    const code = start(["--data", dataDir], (args) => {
      invocation = args;
      return 0;
    }, probes);
    assert.equal(code, 0);
    assert.ok(invocation);
    assert.deepEqual(invocation.slice(-5), [
      "-e", "VM_MAIL_SMARTHOST=relay.example",
      "-e", "VM_MAIL_FLUSH_SEC=15", "mayanklahiri/virtualme:latest",
    ]);
  } finally {
    if (previous === undefined) delete process.env.VM_MAIL_SMARTHOST;
    else process.env.VM_MAIL_SMARTHOST = previous;
    if (previousFlush === undefined) delete process.env.VM_MAIL_FLUSH_SEC;
    else process.env.VM_MAIL_FLUSH_SEC = previousFlush;
    rmSync(parent, { recursive: true, force: true });
  }
});

test("start forwards configured TTS cache environment", () => {
  const parent = mkdtempSync(join(tmpdir(), "virtualme-test-"));
  const dataDir = join(parent, "data");
  const previous = process.env.VM_TTS_CACHE_MAX_MB;
  process.env.VM_TTS_CACHE_MAX_MB = "64";
  try {
    /** @type {string[] | undefined} */
    let invocation;
    const probes = { haveDocker: () => true, daemonUp: () => true, containerState: () => "absent" };
    const code = start(["--data", dataDir], (args) => {
      invocation = args;
      return 0;
    }, probes);
    assert.equal(code, 0);
    assert.ok(invocation);
    assert.deepEqual(invocation.slice(-3), [
      "-e", "VM_TTS_CACHE_MAX_MB=64", "mayanklahiri/virtualme:latest",
    ]);
  } finally {
    if (previous === undefined) delete process.env.VM_TTS_CACHE_MAX_MB;
    else process.env.VM_TTS_CACHE_MAX_MB = previous;
    rmSync(parent, { recursive: true, force: true });
  }
});

test("start forwards an explicit GPU specification with marker and capability env", () => {
  const parent = mkdtempSync(join(tmpdir(), "virtualme-test-"));
  const dataDir = join(parent, "data");
  try {
    /** @type {string[] | undefined} */
    let invocation;
    // Explicit --gpus wins even when detection says no GPU.
    const probes = {
      haveDocker: () => true, daemonUp: () => true, containerState: () => "absent",
      nvidiaGPU: () => false,
    };
    const code = start(["--data", dataDir, "--gpus", "device=0"], (args) => {
      invocation = args;
      return 0;
    }, probes);
    assert.equal(code, 0);
    assert.ok(invocation);
    assert.deepEqual(invocation.slice(-7), [
      "--gpus", "device=0", "-e", "VM_LLAMA_GPU=1",
      "-e", "NVIDIA_DRIVER_CAPABILITIES=all", "mayanklahiri/virtualme:latest",
    ]);
  } finally {
    rmSync(parent, { recursive: true, force: true });
  }
});

test("start auto-passes the whole GPU through when the host has NVIDIA", () => {
  const parent = mkdtempSync(join(tmpdir(), "virtualme-test-"));
  try {
    /** @type {string[] | undefined} */
    let invocation;
    const probes = {
      haveDocker: () => true, daemonUp: () => true, containerState: () => "absent",
      nvidiaGPU: () => true,
    };
    assert.equal(start(["--data", join(parent, "data")], (args) => {
      invocation = args;
      return 0;
    }, probes), 0);
    assert.ok(invocation);
    assert.deepEqual(invocation.slice(-7), [
      "--gpus", "all", "-e", "VM_LLAMA_GPU=1",
      "-e", "NVIDIA_DRIVER_CAPABILITIES=all", "mayanklahiri/virtualme:latest",
    ]);
  } finally {
    rmSync(parent, { recursive: true, force: true });
  }
});

test("start --no-gpu suppresses auto GPU passthrough and conflicts with --gpus", () => {
  const parent = mkdtempSync(join(tmpdir(), "virtualme-test-"));
  try {
    /** @type {string[] | undefined} */
    let invocation;
    const probes = {
      haveDocker: () => true, daemonUp: () => true, containerState: () => "absent",
      nvidiaGPU: () => true,
    };
    assert.equal(start(["--data", join(parent, "data"), "--no-gpu"], (args) => {
      invocation = args;
      return 0;
    }, probes), 0);
    assert.ok(invocation);
    assert.equal(invocation.includes("--gpus"), false);
    assert.equal(invocation.includes("VM_LLAMA_GPU=1"), false);
    assert.equal(start(["--gpus", "all", "--no-gpu"], () => 0, probes), 2);
  } finally {
    rmSync(parent, { recursive: true, force: true });
  }
});

test("start rejects unknown flags as a usage error", () => {
  const probes = { haveDocker: () => true, daemonUp: () => true, containerState: () => "absent" };
  const code = start(["--bogus"], () => 0, probes);
  assert.equal(code, 2);
});

test("soak rejects unknown flags and is listed in help", () => {
  const bad = cli(["soak", "--bogus"]);
  assert.equal(bad.status, 2);
  assert.match(bad.stderr, /usage: soak/);
  const help = cli(["help"]);
  assert.match(help.stdout, /soak \[--no-build\]/);
});
