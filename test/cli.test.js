import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { spawnSync } from "node:child_process";

const pkg = JSON.parse(readFileSync(new URL("../package.json", import.meta.url), "utf8"));

/** @param {string[]} args */
function cli(args) {
  return spawnSync(process.execPath, ["bin/virtualme.js", ...args], { encoding: "utf8" });
}

test("help exits successfully and contains Usage", () => {
  const result = cli(["help"]);
  assert.equal(result.status, 0);
  assert.match(result.stdout, /Usage/);
});

test("no arguments shows help", () => {
  const result = cli([]);
  assert.equal(result.status, 0);
  assert.match(result.stdout, /Usage/);
});

test("version prints package version", () => {
  const result = cli(["version"]);
  assert.equal(result.status, 0);
  assert.equal(result.stdout.trim(), pkg.version);
});

test("unknown command is a usage error", () => {
  const result = cli(["nope"]);
  assert.equal(result.status, 2);
  assert.match(result.stderr, /unknown command/);
});
