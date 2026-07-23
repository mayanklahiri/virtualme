import { CONTAINER } from "../config.js";
import { containerState, run as docker } from "../docker.js";
import { red } from "../ansi.js";

/** @param {string[]} argv */
export function run(argv) {
  if (argv.length > 0) {
    console.error(red("error: stop takes no arguments"));
    return 2;
  }
  if (containerState() === "absent") {
    console.log("Virtual Me container is not present.");
    return 0;
  }
  const stopCode = docker(["stop", CONTAINER]);
  if (stopCode !== 0) return stopCode;
  return docker(["rm", CONTAINER]);
}
