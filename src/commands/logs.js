import { parseArgs } from "node:util";
import { run as docker } from "../docker.js";
import { CONTAINER } from "../config.js";
import { red } from "../ansi.js";

/** @param {string[]} argv */
export function run(argv) {
  try {
    const { values, positionals } = parseArgs({
      args: argv,
      options: { follow: { type: "boolean", short: "f" } },
      allowPositionals: true,
      strict: true,
    });
    if (positionals.length > 0) throw new Error("unexpected argument");
    return docker(["logs", CONTAINER, ...(values.follow ? ["-f"] : [])]);
  } catch (error) {
    console.error(red(`error: ${error instanceof Error ? error.message : String(error)}`));
    return 2;
  }
}
