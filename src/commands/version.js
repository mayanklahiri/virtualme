import { readFileSync } from "node:fs";

/** @param {string[]} _argv */
export function run(_argv) {
  const packageJson = JSON.parse(
    readFileSync(new URL("../../package.json", import.meta.url), "utf8"),
  );
  console.log(packageJson.version);
  return 0;
}
