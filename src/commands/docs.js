import { existsSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { constants } from "node:os";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

export const usage = "Usage:\n  virtualme docs dev [--host <host>] [--port <port>]\n  virtualme docs build";

export class UsageError extends Error {
  /** @param {string} message */
  constructor(message) {
    super(message);
    this.name = "UsageError";
  }
}

/**
 * @typedef {{help: true} | {command: "build"} | {command: "dev", host: string, port: string}} ParsedDocsArgs
 * @typedef {{status?: number | null, signal?: string | null, error?: Error}} ChildResult
 * @typedef {{
 *   exists?: (path: string) => boolean,
 *   spawn?: (file: string, args: string[], options: {cwd: string, stdio: "inherit"}) => ChildResult,
 *   print?: (message: string) => void,
 *   error?: (message: string) => void
 * }} DocsDependencies
 */

/** @param {string[]} argv @returns {ParsedDocsArgs} */
export function parseArgs(argv) {
  if (argv.length === 1 && argv[0] === "--help") return { help: true };
  const [command, ...rest] = argv;
  if (!["dev", "build"].includes(command)) throw new UsageError(command ? `unknown docs command: ${command}` : "missing docs command");
  if (rest.includes("--help")) {
    if (rest.length !== 1) throw new UsageError("help does not accept other arguments");
    return { help: true };
  }
  if (command === "build") {
    if (rest.length) throw new UsageError("docs build accepts no arguments");
    return { command: "build" };
  }
  let host = "127.0.0.1";
  let port = "4321";
  const seen = new Set();
  for (let index = 0; index < rest.length; index += 2) {
    const option = rest[index];
    const value = rest[index + 1];
    if (!["--host", "--port"].includes(option) || seen.has(option) || value === undefined || value === "" || value.startsWith("--")) {
      throw new UsageError(`invalid docs dev option: ${option ?? ""}`);
    }
    seen.add(option);
    if (option === "--host") host = value;
    else {
      if (!/^\d+$/.test(value) || Number(value) < 1 || Number(value) > 65535) throw new UsageError(`invalid port: ${value}`);
      port = value;
    }
  }
  return { command: "dev", host, port };
}

const docsDirectory = resolve(dirname(fileURLToPath(import.meta.url)), "../../docs");

/** @param {string[]} argv @param {DocsDependencies} [dependencies] */
export async function runWith(argv, dependencies = {}) {
  const exists = dependencies.exists ?? existsSync;
  const spawn = dependencies.spawn ?? ((file, args, options) => spawnSync(file, args, options));
  const print = dependencies.print ?? console.log;
  const error = dependencies.error ?? console.error;
  let parsed;
  try { parsed = parseArgs(argv); } catch (caught) {
    error(caught instanceof Error ? caught.message : String(caught));
    error(usage);
    return 2;
  }
  if ("help" in parsed) {
    print(usage);
    return 0;
  }
  if (!exists(resolve(docsDirectory, "package.json"))) {
    error("docs requires a virtualme source checkout");
    return 1;
  }
  if (!exists(resolve(docsDirectory, "node_modules/.bin/astro"))) {
    error("docs dependencies missing; run npm ci --prefix docs");
    return 1;
  }
  const args = parsed.command === "build"
    ? ["run", "build"]
    : ["run", "dev", "--", "--host", parsed.host, "--port", parsed.port];
  let child;
  try { child = spawn("npm", args, { cwd: docsDirectory, stdio: "inherit" }); } catch (caught) {
    error(`docs failed to start: ${caught instanceof Error ? caught.message : String(caught)}`);
    return 1;
  }
  if (child?.error) {
    error(`docs failed to start: ${child.error.message}`);
    return 1;
  }
  if (child?.signal) {
    const signal = /** @type {keyof typeof constants.signals} */ (child.signal);
    return 128 + (constants.signals[signal] ?? 0);
  }
  return child?.status === 0 ? 0 : 1;
}

/** @param {string[]} argv */
export function run(argv) {
  return runWith(argv);
}
