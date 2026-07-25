import assert from "node:assert/strict";
import test from "node:test";

import { classifyResult, parseEnvBlocks } from "../controller/web/static/js/tools-render.js";

test("page-shaped JSON results are classified by shape, not tool name", () => {
  const payload = JSON.stringify({
    url: "https://example.test/article",
    title: "An Example Article",
    text: "First paragraph.\n\nSecond paragraph.",
  });
  const result = classifyResult(payload);
  assert.equal(result.kind, "page");
  assert.equal(result.url, "https://example.test/article");
  assert.equal(result.title, "An Example Article");
  assert.equal(result.text, "First paragraph.\n\nSecond paragraph.");
  // A subset of the page keys still qualifies.
  const partial = classifyResult(JSON.stringify({ url: "http://a.test", text: "t" }));
  assert.equal(partial.kind, "page");
});

test("non-page payloads keep their generic kinds", () => {
  assert.equal(classifyResult(JSON.stringify({ url: "https://a.test", extra: 1 })).kind, "json");
  assert.equal(classifyResult(JSON.stringify({ url: "ftp://a.test", text: "x" })).kind, "json");
  assert.equal(classifyResult(JSON.stringify({ url: 42, text: "x" })).kind, "json");
  assert.equal(classifyResult(JSON.stringify([1, 2])).kind, "json");
  assert.equal(classifyResult("plain words").kind, "text");
});

test("runs of three or more KEY=value lines become sorted env tables", () => {
  const text = [
    "System summary",
    "PATH=/usr/bin",
    "HOME=/home/me",
    "VM_DISPLAY=:99",
    "",
    "trailing note",
  ].join("\n");
  const segments = parseEnvBlocks(text);
  assert.equal(segments.length, 3);
  assert.equal(segments[0].kind, "text");
  assert.equal(segments[1].kind, "env");
  assert.deepEqual(segments[1].entries, [
    ["HOME", "/home/me"],
    ["PATH", "/usr/bin"],
    ["VM_DISPLAY", ":99"],
  ]);
  assert.equal(segments[2].kind, "text");
  assert.ok(segments[2].text.includes("trailing note"));
});

test("fewer than three env lines stay plain text", () => {
  const segments = parseEnvBlocks("A=1\nB=2\nnot an env line");
  assert.equal(segments.length, 1);
  assert.equal(segments[0].kind, "text");
});
