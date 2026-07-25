import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import vm from "node:vm";
import { createDOMStub } from "./helpers/dom-stub.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const exprPath = path.join(root, "test/fixtures/dom-query-img.expr.txt");
const fixturePath = path.join(root, "test/fixtures/lahiri-me.dom.json");

test("dom_query img expression runs without null.map against lahiri fixture", () => {
  const expression = readFileSync(exprPath, "utf8").trim();
  assert.doesNotMatch(expression, /null\.map/);
  assert.match(expression, /\[\]\.map/);

  const fixture = JSON.parse(readFileSync(fixturePath, "utf8"));
  const sandbox = createDOMStub(fixture);
  const context = vm.createContext({
    document: sandbox.document,
    location: sandbox.location,
    window: sandbox.window,
    console,
  });
  const result = JSON.parse(vm.runInContext(expression, context));
  assert.ok(Array.isArray(result));
  assert.ok(result.length >= 1);
  assert.equal(result[0].tag, "img");
});
