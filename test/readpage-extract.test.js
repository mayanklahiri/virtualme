import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { runReadPage } from "./helpers/dom-stub.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const fixtures = path.join(root, "test/fixtures");

/** @param {any} nodes @param {(node: any) => void} fn */
function walkNodes(nodes, fn) {
  for (const node of nodes ?? []) {
    fn(node);
    walkNodes(node.children, fn);
    walkNodes(node.items, fn);
  }
}

test("read_page extracts example.com structure deterministically", () => {
  const fixture = JSON.parse(readFileSync(path.join(fixtures, "example-com.dom.json"), "utf8"));
  const first = runReadPage(fixture);
  const second = runReadPage(fixture);
  // Results come from separate vm realms; compare via JSON for determinism.
  assert.equal(JSON.stringify(first), JSON.stringify(second));
  assert.equal(first.title, "Example Domain");
  assert.equal(first.url, "https://example.com/");
  assert.ok(Array.isArray(first.body));
  let headings = 0;
  let links = 0;
  walkNodes(first.body, (node) => {
    assert.ok(node.tag);
    assert.ok(node.sel);
    if (node.tag === "h1") headings++;
    if (node.href) {
      links++;
      assert.match(node.href, /^https:\/\//);
    }
  });
  assert.equal(headings, 1);
  assert.equal(links, 1);
});

test("read_page extracts lahiri.me links and headings", () => {
  const fixture = JSON.parse(readFileSync(path.join(fixtures, "lahiri-me.dom.json"), "utf8"));
  const digest = runReadPage(fixture);
  assert.match(digest.title, /Lahiri/i);
  let links = 0;
  walkNodes(digest.body, (node) => {
    if (node.href) links++;
  });
  assert.ok(links >= 2);
});
