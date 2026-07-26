import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const expectedPins = [
  "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7",
  "actions/setup-node@820762786026740c76f36085b0efc47a31fe5020 # v7",
];

test("docs workflow has bounded triggers and permissions", async () => {
  const yaml = await readFile(new URL("../.github/workflows/docs.yml", import.meta.url), "utf8");
  for (const path of [
    "docs/**", "common/themes/**", "controller/internal/config/**", "controller/cmd/configctl/**",
    "scripts/generate-themes.mjs", "src/commands/docs.js", "src/main.js",
    "src/commands/help.js", "test/docs*.test.js", ".github/workflows/docs.yml",
  ]) assert.equal((yaml.match(new RegExp(`"${path.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}"`, "g")) ?? []).length, 2);
  assert.match(yaml, /branches: \[main\]/);
  assert.match(yaml, /group: docs-\$\{\{ github\.ref \}\}/);
  assert.match(yaml, /cancel-in-progress: true/);
  assert.match(yaml, /build:[\s\S]*?permissions:\s*\{contents: read\}/);
  assert.match(yaml, /deploy:[\s\S]*?if: github\.event_name == 'push' && github\.ref == 'refs\/heads\/main'/);
  assert.match(yaml, /deploy:[\s\S]*?needs: build[\s\S]*?permissions:\s*\{contents: write\}/);
});

test("docs workflow pins official actions and performs explicit setup", async () => {
  const yaml = await readFile(new URL("../.github/workflows/docs.yml", import.meta.url), "utf8");
  for (const pin of expectedPins) assert.match(yaml, new RegExp(pin.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")));
  for (const use of yaml.matchAll(/uses:\s*(\S+)/g)) assert.match(use[1], /^actions\/(?:checkout|setup-node)@[0-9a-f]{40}$/);
  assert.match(yaml, /node-version: 24/);
  assert.match(yaml, /npm ci\n/);
  assert.match(yaml, /npm ci --prefix docs/);
  assert.match(yaml, /playwright install --with-deps chromium/);
  assert.doesNotMatch(yaml, /upload-pages|deploy-pages|peaceiris|api\/repos\/.*pages/);
});

test("deployment is a plain-Git orphan branch-root force publication", async () => {
  const yaml = await readFile(new URL("../.github/workflows/docs.yml", import.meta.url), "utf8");
  assert.match(yaml, /cp -a docs\/dist\/\. /);
  assert.match(yaml, /git init --initial-branch=docs/);
  assert.match(yaml, /docs: deploy \$\{GITHUB_SHA\}/);
  assert.match(yaml, /git push --force origin HEAD:docs/);
  assert.doesNotMatch(yaml, /HEAD:main|branches:\s*\[docs\]/);
});
