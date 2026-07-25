import assert from "node:assert/strict";
import { existsSync, readdirSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import test from "node:test";
import { pathToFileURL } from "node:url";
import { evaluateProperties } from "./helpers/digest-props.mjs";
import { digestYAML } from "./helpers/digest-yaml.mjs";
import { runReadPage } from "./helpers/dom-stub.mjs";

const fixtures = path.resolve("test/fixtures");
const domFiles = readdirSync(fixtures).filter((name) => name.endsWith(".dom.json")).sort();

for (const file of domFiles) {
  const base = file.slice(0, -".dom.json".length);
  test(`read_page golden: ${base}`, async () => {
    const fixture = JSON.parse(readFileSync(path.join(fixtures, file), "utf8"));
    const digest = runReadPage(fixture);
    const yaml = digestYAML(digest);
    const golden = path.join(fixtures, `${base}.digest.golden.yaml`);
    if (process.env.REGEN_GOLDENS === "1") writeFileSync(golden, yaml);
    assert.equal(yaml, readFileSync(golden, "utf8"));
    const props = path.join(fixtures, `${base}.props.mjs`);
    if (existsSync(props)) {
      const module = await import(pathToFileURL(props).href);
      evaluateProperties(module.default, digest, yaml);
    }
  });
}
