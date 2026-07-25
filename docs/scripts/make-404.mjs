import { readFile, writeFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const docs = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const source = resolve(docs, "dist/404.html");
const destination = resolve(docs, "dist/404.html");
let html;
try { html = await readFile(source, "utf8"); } catch { throw new Error("rendered 404 route is absent"); }
const rewritten = html.replace(
  /<link rel="canonical" href="[^"]+">/,
  '<link rel="canonical" href="https://mayanklahiri.github.io/virtualme/404.html">',
);
if (rewritten === html) throw new Error("404 canonical was not found");
await writeFile(destination, rewritten);
