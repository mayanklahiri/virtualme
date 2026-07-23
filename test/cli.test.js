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
      "mayanklahiri/virtualme:latest",
    ]);
  } finally {
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
  process.env.VM_MAIL_SMARTHOST = "relay.example";
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
      "-e", "VM_MAIL_SMARTHOST=relay.example", "mayanklahiri/virtualme:latest",
    ]);
  } finally {
    if (previous === undefined) delete process.env.VM_MAIL_SMARTHOST;
    else process.env.VM_MAIL_SMARTHOST = previous;
    rmSync(parent, { recursive: true, force: true });
  }
});

test("start forwards an optional GPU specification and marker env", () => {
  const parent = mkdtempSync(join(tmpdir(), "virtualme-test-"));
  const dataDir = join(parent, "data");
  try {
    /** @type {string[] | undefined} */
    let invocation;
    const probes = { haveDocker: () => true, daemonUp: () => true, containerState: () => "absent" };
    const code = start(["--data", dataDir, "--gpus", "all"], (args) => {
      invocation = args;
      return 0;
    }, probes);
    assert.equal(code, 0);
    assert.ok(invocation);
    assert.deepEqual(invocation.slice(-5), [
      "--gpus", "all", "-e", "VM_LLAMA_GPU=1", "mayanklahiri/virtualme:latest",
    ]);
  } finally {
    rmSync(parent, { recursive: true, force: true });
  }
});

test("start rejects unknown flags as a usage error", () => {
  const probes = { haveDocker: () => true, daemonUp: () => true, containerState: () => "absent" };
  const code = start(["--bogus"], () => 0, probes);
  assert.equal(code, 2);
});
