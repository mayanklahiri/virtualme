import { IMAGE, TAG } from "../config.js";
import { run as docker } from "../docker.js";
import { red } from "../ansi.js";

/** @param {string[]} argv */
export function run(argv) {
  if (argv.length > 0) {
    console.error(red("error: update takes no arguments"));
    return 2;
  }
  return docker(["pull", `${IMAGE}:${TAG}`]);
}
