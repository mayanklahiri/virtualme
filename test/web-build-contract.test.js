// @ts-nocheck
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

test("web build bundles generated theme CSS into the runtime stylesheet", async () => {
  const [build, css] = await Promise.all([
    readFile(new URL("../scripts/build-web.sh", import.meta.url), "utf8"),
    readFile(new URL("../controller/web/static/css/app.css", import.meta.url), "utf8"),
  ]);

  assert.match(build, /"\$ESBUILD" "\$SRC\/css\/app\.css" --bundle "--external:\/fonts\/\*" --minify/);
  assert.match(build, /bundled CSS contains unresolved @import/);
  assert.match(build, /bundled CSS is missing generated theme tokens/);
  assert.match(build, /bundled CSS is missing local font declarations/);
  assert.match(css, /^@import "\.\/generated-themes\.css";/);
});
