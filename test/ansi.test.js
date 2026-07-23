import test from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { red, wrap } from "../src/ansi.js";

test("NO_COLOR disables ANSI output in a child process", () => {
  const result = spawnSync(
    process.execPath,
    ["--input-type=module", "-e", "import { red } from './src/ansi.js'; process.stdout.write(red('x'));"],
    { encoding: "utf8", env: { ...process.env, NO_COLOR: "1" } },
  );
  assert.equal(result.status, 0);
  assert.equal(result.stdout, "x");
});

test("wrap returns plain text when colors are disabled", () => {
  assert.equal(wrap(31)("x"), "x");
  assert.equal(red("x"), "x");
});
