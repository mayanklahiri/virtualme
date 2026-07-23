import { existsSync } from "node:fs";
import { arch, totalmem } from "node:os";
import { spawnSync } from "node:child_process";
import { daemonUp, haveDocker } from "../docker.js";
import { green, red, yellow } from "../ansi.js";

/** @param {string[]} argv */
export function run(argv) {
  if (argv.length > 0) {
    console.error(red("error: doctor takes no arguments"));
    return 2;
  }

  let failed = false;
  /** @param {string} name @param {boolean} ok @param {string} [hint] */
  const check = (name, ok, hint) => {
    console.log(`${ok ? green("ok") : red("FAIL")} ${name}`);
    if (!ok) {
      failed = true;
      if (hint) console.log(`  ${hint}`);
    }
  };

  const nodeMajor = Number.parseInt(process.versions.node.split(".")[0], 10);
  check(`Node >= 22 (${process.versions.node})`, nodeMajor >= 22);
  const docker = haveDocker();
  check("docker on PATH", docker);
  check("docker daemon reachable", docker && daemonUp());

  if (existsSync("scripts/check.sh")) {
    const hook = spawnSync("git", ["config", "core.hooksPath"], { encoding: "utf8" });
    check(
      "git core.hooksPath is .githooks",
      hook.status === 0 && hook.stdout.trim() === ".githooks",
      "run: git config core.hooksPath .githooks",
    );
  }

  const gib = totalmem() / 1024 ** 3;
  const memory = `system: ${arch()}, ${gib.toFixed(1)} GiB RAM`;
  console.log(gib < 8
    ? yellow(`${memory} (warning: Gemma 4 E2B needs ~4 GiB free)`)
    : memory);
  return failed ? 1 : 0;
}
