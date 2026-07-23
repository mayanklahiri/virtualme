import { existsSync } from "node:fs";
import { IMAGE } from "../config.js";
import { run as docker } from "../docker.js";
import { red } from "../ansi.js";

/** @param {string[]} argv */
export function run(argv) {
  if (argv.length > 0) {
    console.error(red("error: build takes no arguments"));
    return 2;
  }
  if (!existsSync("docker/Dockerfile")) {
    console.error(red("build must run from a source checkout (see spec 002)"));
    return 1;
  }
  return docker(["build", "-f", "docker/Dockerfile", "-t", `${IMAGE}:dev`, "."]);
}
