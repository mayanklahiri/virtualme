import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const root = new URL("../controller/web/static/", import.meta.url);

test("console markup declares no live regions (no announcement sounds)", async () => {
  const html = await readFile(new URL("index.html", root), "utf8");
  assert.doesNotMatch(html, /aria-live/);
  assert.doesNotMatch(html, /role="status"/);
  assert.doesNotMatch(html, /role="alert"/);
});
