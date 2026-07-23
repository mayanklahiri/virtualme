import { CONTAINER, IMAGE, PORT, TAG, VOLUME } from "../config.js";
import { containerState, daemonUp, haveDocker, run as docker } from "../docker.js";
import { green, red } from "../ansi.js";

/** @param {string[]} argv */
export function run(argv) {
  if (argv.length > 0) {
    console.error(red("error: start takes no arguments"));
    return 2;
  }
  if (!haveDocker()) {
    console.error(red("error: docker is not installed or not on PATH"));
    return 1;
  }
  if (!daemonUp()) {
    console.error(red("error: docker daemon is not reachable"));
    return 1;
  }
  const state = containerState();
  if (state === "running") {
    console.error(red("error: virtualme is already running"));
    return 1;
  }
  if (state === "exited" && docker(["rm", CONTAINER]) !== 0) return 1;
  const code = docker([
    "run", "-d", "--name", CONTAINER, "--restart", "unless-stopped",
    "--shm-size=1g", "-p", `${PORT}:${PORT}`, "-v", `${VOLUME}:/data`,
    `${IMAGE}:${TAG}`,
  ]);
  if (code === 0) {
    console.log(green(`Virtual Me is running at http://localhost:${PORT}`));
    console.log("Follow startup with: virtualme logs -f");
  }
  return code;
}
