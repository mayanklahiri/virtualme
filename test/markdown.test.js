import assert from "node:assert/strict";
import test from "node:test";

import { parseTable } from "../controller/web/static/js/markdown-table.js";

test("parseTable recognizes a basic GFM pipe table", () => {
  const parsed = parseTable([
    "| Name | Value |",
    "| --- | --- |",
    "| cpu | 42 |",
    "| mem | 7 |",
  ]);
  assert.ok(parsed);
  assert.deepEqual(parsed.header, ["Name", "Value"]);
  assert.deepEqual(parsed.align, [null, null]);
  assert.deepEqual(parsed.rows, [["cpu", "42"], ["mem", "7"]]);
  assert.equal(parsed.consumed, 4);
});

test("parseTable reads alignment colons", () => {
  const parsed = parseTable([
    "| a | b | c |",
    "|:---|:---:|---:|",
    "| 1 | 2 | 3 |",
  ]);
  assert.ok(parsed);
  assert.deepEqual(parsed.align, ["left", "center", "right"]);
});

test("parseTable accepts tables without outer pipes", () => {
  const parsed = parseTable([
    "Name | Value",
    "--- | ---",
    "cpu | 42",
  ]);
  assert.ok(parsed);
  assert.deepEqual(parsed.header, ["Name", "Value"]);
  assert.deepEqual(parsed.rows, [["cpu", "42"]]);
});

test("parseTable keeps escaped pipes literal", () => {
  const parsed = parseTable([
    "| Expr | Out |",
    "| --- | --- |",
    "| a \\| b | ok |",
  ]);
  assert.ok(parsed);
  assert.deepEqual(parsed.rows, [["a | b", "ok"]]);
});

test("parseTable pads and truncates ragged rows to the header width", () => {
  const parsed = parseTable([
    "| a | b |",
    "| --- | --- |",
    "| 1 |",
    "| 1 | 2 | 3 |",
  ]);
  assert.ok(parsed);
  assert.deepEqual(parsed.rows, [["1", ""], ["1", "2"]]);
});

test("parseTable rejects non-tables", () => {
  assert.equal(parseTable(["just | a line", "of plain text"]), null);
  assert.equal(parseTable(["| solo header |"]), null);
  assert.equal(parseTable(["a | b", "not a separator"]), null);
});

test("parseTable stops at the first non-row line", () => {
  const parsed = parseTable([
    "| a | b |",
    "| --- | --- |",
    "| 1 | 2 |",
    "",
    "paragraph after",
  ]);
  assert.ok(parsed);
  assert.equal(parsed.consumed, 3);
  assert.deepEqual(parsed.rows, [["1", "2"]]);
});
