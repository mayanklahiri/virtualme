import { PORT } from "../config.js";
import { containerState } from "../docker.js";
import { green, red, yellow } from "../ansi.js";

/** @param {string[]} argv */
export async function run(argv) {
  if (argv.length > 0) {
    console.error(red("error: status takes no arguments"));
    return 2;
  }
  const state = containerState();
  console.log(`container: ${state}`);
  if (state !== "running") return 0;
  try {
    const response = await fetch(`http://127.0.0.1:${PORT}/healthz`, {
      signal: globalThis.AbortSignal.timeout(2000),
    });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    /** @type {unknown} */
    const health = await response.json();
    if (health && typeof health === "object" && "services" in health &&
        Array.isArray(health.services)) {
      for (const service of health.services) {
        if (service && typeof service === "object" &&
            "name" in service && typeof service.name === "string" &&
            "ok" in service) {
          console.log(`${service.name}: ${service.ok === true ? green("ok") : red("FAIL")}`);
        }
      }
    }
  } catch {
    console.log(yellow("controller: unreachable"));
  }
  return 0;
}
