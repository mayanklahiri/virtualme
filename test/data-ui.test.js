import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const root = new URL("../controller/web/static/", import.meta.url);
const dataModule = new URL("js/data.js", root);

test("Data page is wired into the SPA router and markup", async () => {
  const [html, app, router, data] = await Promise.all([
    readFile(new URL("index.html", root), "utf8"),
    readFile(new URL("js/app.js", root), "utf8"),
    readFile(new URL("js/router.js", root), "utf8"),
    readFile(new URL("js/data.js", root), "utf8"),
  ]);
  assert.match(html, /data-page="data"/);
  assert.match(html, /href="\/data"/);
  assert.match(html, /Browse your data\. Stored on your machine, not in a data center\./);
  assert.match(html, /id="data-tree"/);
  assert.match(html, /id="data-viewer"/);
  assert.match(html, /id="data-breadcrumbs"/);
  assert.match(html, /id="data-splitter"/);
  assert.match(html, /data-data-sort="name"/);
  assert.match(html, /data-data-view="icons"/);
  assert.match(html, /icons\.svg#i-download|icons\.svg#i-folder/);
  assert.match(router, /\["\/data", \["data", "Data"\]\]/);
  assert.match(app, /initData/);
  assert.match(app, /data\.show\(page\)/);
  assert.match(data, /\/api\/data\/list/);
  assert.match(data, /\/api\/data\/file/);
  assert.match(data, /\/api\/data\/du/);
  assert.match(data, /vm-data-view/);
  assert.match(data, /vm-data-sort/);
  assert.match(data, /vm-data-split/);
  assert.match(data, /pushState/);
  assert.match(data, /popstate/);
  assert.doesNotMatch(data, /innerHTML/);
});

test("Data path sanitizing, root filtering, and sorting are deterministic", async () => {
  const {DATA_ROOT_IGNORED, sanitizeDataPath, sortDataEntries} = await import(dataModule.href);
  assert.deepEqual([...DATA_ROOT_IGNORED], ["chromium", "mail", "metrics", "valkey", "xdg"]);
  assert.equal(sanitizeDataPath("agent/task/steps.jsonl"), "agent/task/steps.jsonl");
  assert.equal(sanitizeDataPath("agent//./task"), "agent/task");
  assert.equal(sanitizeDataPath("../etc/passwd"), null);
  assert.equal(sanitizeDataPath("/etc/passwd"), null);
  assert.equal(sanitizeDataPath(String.raw`..\etc\passwd`), null);

  /** @type {Array<{name:string, kind:"dir"|"file", size:number, mtimeMs:number}>} */
  const entries = [
    {name: "z", kind: "file", size: 2, mtimeMs: 2},
    {name: "b", kind: "dir", size: 0, mtimeMs: 3},
    {name: "a", kind: "dir", size: 0, mtimeMs: 1},
    {name: "a", kind: "file", size: 8, mtimeMs: 1},
  ];
  assert.deepEqual(
    sortDataEntries(entries, "name", "asc").map(
      (/** @type {{name:string, kind:string}} */ item) => `${item.kind}:${item.name}`,
    ),
    ["dir:a", "dir:b", "file:a", "file:z"],
  );
  assert.deepEqual(
    sortDataEntries(entries, "size", "desc", new Map([["a", 3], ["b", 9]]))
      .map((/** @type {{name:string, kind:string}} */ item) => `${item.kind}:${item.name}`),
    ["dir:b", "dir:a", "file:a", "file:z"],
  );
});
