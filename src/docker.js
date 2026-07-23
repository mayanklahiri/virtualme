import { spawnSync } from "node:child_process";
import { CONTAINER } from "./config.js";

export function haveDocker() {
  const result = spawnSync("docker", ["--version"], { stdio: "ignore" });
  return result.status === 0;
}

export function daemonUp() {
  const result = spawnSync("docker", ["info"], { stdio: "ignore" });
  return result.status === 0;
}

/**
 * @param {string[]} args
 * @param {{ inherit?: boolean }} [options]
 */
export function run(args, { inherit = true } = {}) {
  const result = spawnSync("docker", args, {
    stdio: inherit ? "inherit" : "ignore",
  });
  return result.status ?? 1;
}

/**
 * @param {string[]} args
 * @returns {{ code: number, stdout: string }}
 */
export function capture(args) {
  const result = spawnSync("docker", args, { encoding: "utf8" });
  return { code: result.status ?? 1, stdout: result.stdout ?? "" };
}

/** @returns {"running" | "exited" | "absent"} */
export function containerState() {
  const result = capture(["inspect", "-f", "{{.State.Status}}", CONTAINER]);
  if (result.code !== 0) return "absent";
  return result.stdout.trim() === "running" ? "running" : "exited";
}
