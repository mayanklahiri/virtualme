import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const root = new URL("../controller/web/static/", import.meta.url);

test("Tools page is server-driven and schema-generated", async () => {
  const [html, app, router, tools, css] = await Promise.all([
    readFile(new URL("index.html", root), "utf8"),
    readFile(new URL("js/app.js", root), "utf8"),
    readFile(new URL("js/router.js", root), "utf8"),
    readFile(new URL("js/tools.js", root), "utf8"),
    readFile(new URL("css/app.css", root), "utf8"),
  ]);
  assert.match(html, /data-page="tools"/);
  assert.match(html, /id="tools-list"/);
  assert.match(tools, /Trusted-network console/);
  assert.match(router, /\["\/tools", \["tools", "Tools"\]\]/);
  assert.match(app, /case "tools-list":/);
  assert.match(app, /case "tool-result":/);
  assert.match(tools, /Object\.entries\(properties\)/);
  assert.match(tools, /JSON\.parse\(value\)/);
  assert.match(tools, /queued…/);
  assert.match(tools, /120000/);
  assert.match(css, /grid-template-columns:16rem minmax\(0,1fr\) 24rem/);
  assert.doesNotMatch(tools, /dom_query|dom_validate|page_eval|layout_debug/);
});
