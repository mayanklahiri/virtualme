import { existsSync } from "node:fs";
import { IMAGE, TAG } from "../config.js";
import { run as runDocker } from "../docker.js";
import { red } from "../ansi.js";

/**
 * @param {string[]} argv
 * @param {(args: string[]) => number} [docker]
 */
export function run(argv, docker = runDocker) {
  if (argv.length > 0) {
    console.error(red("error: build takes no arguments"));
    return 2;
  }
  if (!existsSync("docker/Dockerfile")) {
    console.error(red("build must run from a source checkout (see spec 002)"));
    return 1;
  }
  const args = ["build", "-f", "docker/Dockerfile", "-t", `${IMAGE}:dev`];
  if (TAG !== "dev") {
    args.push("-t", `${IMAGE}:${TAG}`);
  }
  args.push(".");
  return docker(args);
}
