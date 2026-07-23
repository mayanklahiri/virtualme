import { mkdirSync } from "node:fs";
import { homedir } from "node:os";
import { join, resolve } from "node:path";
import { parseArgs } from "node:util";
import { CONTAINER, DATA_MOUNT, IMAGE, PORT, TAG } from "../config.js";
import { containerState, daemonUp, haveDocker, run as runDocker } from "../docker.js";
import { green, red } from "../ansi.js";

/**
 * Resolve the host data directory: --data flag > VIRTUALME_DATA > ~/.virtualme.
 * @param {string | undefined} flag
 */
function resolveDataDir(flag) {
  return resolve(flag ?? process.env.VIRTUALME_DATA ?? join(homedir(), ".virtualme"));
}

/**
 * @param {string[]} argv
 * @param {(args: string[]) => number} [docker]
 * @param {{ haveDocker: () => boolean, daemonUp: () => boolean, containerState: () => string }} [probes]
 */
export function run(argv, docker = runDocker, probes = { haveDocker, daemonUp, containerState }) {
  /** @type {{ data?: string }} */
  let flags;
  try {
    flags = parseArgs({ args: argv, options: { data: { type: "string" } } }).values;
  } catch {
    console.error(red("error: usage: start [--data <dir>]"));
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
  const code = docker([
    "run", "-d", "--name", CONTAINER, "--restart", "unless-stopped",
    "--shm-size=1g",
    "--user", `${uid}:${gid}`,
    "--tmpfs", `/run:exec,mode=755,uid=${uid},gid=${gid}`,
    "--tmpfs", "/tmp:mode=1777",
    "-p", `${PORT}:${PORT}`,
    "-v", `${dataDir}:${DATA_MOUNT}`,
    `${IMAGE}:${TAG}`,
  ]);
  if (code === 0) {
    console.log(green(`Virtual Me is running at http://localhost:${PORT}`));
    console.log(`Data directory: ${dataDir}`);
    console.log("Follow startup with: virtualme logs -f");
  }
  return code;
}
