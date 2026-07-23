import test from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";

function keygen() {
  const result = spawnSync(process.execPath, ["bin/virtualme.js", "keygen"], { encoding: "utf8" });
  assert.equal(result.status, 0);
  return result.stdout.trim();
}

test("keygen emits unique 256-bit base64url tokens", () => {
  const first = keygen();
  const second = keygen();
  assert.match(first, /^[A-Za-z0-9_-]{43}$/);
  assert.match(second, /^[A-Za-z0-9_-]{43}$/);
  assert.notEqual(first, second);
});
