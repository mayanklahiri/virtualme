// @ts-nocheck
import assert from "node:assert/strict";
import { mkdtemp, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

const expected = ["modern", "editorial", "terminal", "warm", "contrast", "arctic", "solar", "studio"];

test("theme source validates and generated outputs have exact parity", async () => {
  const { loadThemes, renderOutputs } = await import("../scripts/generate-themes.mjs");
  const source = JSON.parse(await readFile(new URL("../common/themes/themes.json", import.meta.url), "utf8"));
  const normalized = loadThemes(source);
  assert.deepEqual(normalized.themes.map(({ id }) => id), expected);
  const outputs = renderOutputs(normalized);
  assert.equal(outputs.size, 5);
  const bytes = [...outputs.values()].join("\n");
  for (const id of expected) assert.match(bytes, new RegExp(id));
  for (let i = 1; i <= 8; i += 1) assert.match(bytes, new RegExp(`--p${i}:`));
  assert.doesNotMatch(bytes, /\d{4}-\d{2}-\d{2}T|\/home\//);
  assert.deepEqual(renderOutputs(normalized), outputs);
  const picker = await readFile(new URL("../docs/src/scripts/theme-picker.ts", import.meta.url), "utf8");
  assert.doesNotMatch(picker, /const themes\s*=\s*\[/);
  for (const id of expected.slice(1)) assert.doesNotMatch(picker, new RegExp(`["']${id}["']`), `authored picker duplicates ${id}`);
  assert.match(picker, /data-theme-value/);
});

test("theme schema rejects malformed records and unknown arguments", async () => {
  const { loadThemes, main } = await import("../scripts/generate-themes.mjs");
  const source = JSON.parse(await readFile(new URL("../common/themes/themes.json", import.meta.url), "utf8"));
  for (const mutate of [
    (x) => { delete x.defaultTheme; },
    (x) => { x.unknown = true; },
    (x) => { x.themes[1].id = x.themes[0].id; },
    (x) => { x.themes[0].light.bg = "#FFF"; },
    (x) => { x.themes[0].shape.radius = "10"; },
    (x) => { x.themes[0].typography.scale = 0; },
    (x) => { x.themes.reverse(); },
    (x) => { x.themes[0].light.unknown = "#000000"; },
  ]) {
    const copy = structuredClone(source);
    mutate(copy);
    assert.throws(() => loadThemes(copy), /\/|order|duplicate/i);
  }
  assert.equal(await main(["--wat"], { error: () => {} }), 2);
});

test("check mode is read-only and reports every stale output", async () => {
  const { loadThemes, renderOutputs, synchronize } = await import("../scripts/generate-themes.mjs");
  const source = loadThemes(JSON.parse(await readFile(new URL("../common/themes/themes.json", import.meta.url), "utf8")));
  const dir = await mkdtemp(join(tmpdir(), "virtualme-themes-"));
  const destinations = new Map([...renderOutputs(source)].map(([name, value]) => [join(dir, name.replaceAll("/", "_")), value]));
  assert.equal(await synchronize(destinations, { check: false }), 0);
  const stale = [...destinations.keys()].slice(0, 2);
  await Promise.all(stale.map((path) => writeFile(path, "stale\n")));
  const reported = [];
  assert.equal(await synchronize(destinations, { check: true, error: (value) => reported.push(value) }), 1);
  assert.deepEqual(reported.sort(), stale.sort());
  assert.equal(await readFile(stale[0], "utf8"), "stale\n");
});
