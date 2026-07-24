import { spawnSync } from "node:child_process";
import { existsSync } from "node:fs";
import { red } from "../ansi.js";

/**
 * Run the soak suite (spec 012): rebuild, restart on a fresh data dir, and
 * exercise end-to-end feature flows against the live controller.
 * Source-checkout-only workflow; not meaningful for npm consumers.
 * @param {string[]} argv
 */
export function run(argv) {
  for (const arg of argv) {
    if (arg !== "--no-build") {
      console.error(red("error: usage: soak [--no-build]"));
      return 2;
    }
  }
  if (!existsSync("test/soak.sh")) {
    console.error(red("soak must run from a source checkout (test/soak.sh not found)"));
    return 1;
  }
  const child = spawnSync("bash", ["test/soak.sh", ...argv], { stdio: "inherit" });
  return child.status ?? 1;
}
