import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const root = new URL("../controller/web/static/", import.meta.url);

test("Data page is wired into the SPA router and markup", async () => {
  const [html, app, router, data] = await Promise.all([
    readFile(new URL("index.html", root), "utf8"),
    readFile(new URL("js/app.js", root), "utf8"),
    readFile(new URL("js/router.js", root), "utf8"),
    readFile(new URL("js/data.js", root), "utf8"),
  ]);
  assert.match(html, /data-page="data"/);
  assert.match(html, /href="\/data"/);
  assert.match(html, /id="data-tree"/);
  assert.match(html, /id="data-viewer"/);
  assert.match(router, /\["\/data", \["data", "Data"\]\]/);
  assert.match(app, /initData/);
  assert.match(app, /data\.show\(page\)/);
  assert.match(data, /\/api\/data\/list/);
  assert.match(data, /\/api\/data\/file/);
  assert.doesNotMatch(data, /innerHTML/);
});
