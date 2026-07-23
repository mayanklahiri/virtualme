import { mkdir, readdir, readFile, writeFile } from "node:fs/promises";
import { basename, join } from "node:path";

const sources = [
  new URL("../controller/web/static/icons/", import.meta.url),
  new URL("../controller/web/static/brand/", import.meta.url),
];
const destination = new URL("../controller/web/dist/icons.svg", import.meta.url);
const files = (await Promise.all(sources.map(async (source) =>
  (await readdir(source))
    .filter((name) => name.endsWith(".svg") && name !== "favicon.svg")
    .map((name) => ({ name, source }))
))).flat()
  .sort((a, b) => a.name.localeCompare(b.name));

if (files.length === 0) {
  throw new Error("build-icons: no SVG sources; run controller/tools/fetch-assets.sh");
}

const symbols = [];
for (const file of files) {
  const svg = await readFile(join(file.source.pathname, file.name), "utf8");
  const match = svg.match(/<svg\b[^>]*>([\s\S]*?)<\/svg>\s*$/);
  if (!match) {
    throw new Error(`build-icons: malformed ${file.name}`);
  }
  const name = basename(file.name, ".svg");
  symbols.push(`<symbol id="i-${name}" viewBox="0 0 24 24">${match[1].trim()}</symbol>`);
}

await mkdir(new URL("../controller/web/dist/", import.meta.url), { recursive: true });
await writeFile(destination, `<svg xmlns="http://www.w3.org/2000/svg">${symbols.join("")}</svg>\n`);
