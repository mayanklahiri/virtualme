import { mkdir, readdir, readFile, writeFile } from "node:fs/promises";
import { basename, join } from "node:path";

const source = new URL("../controller/web/static/icons/", import.meta.url);
const destination = new URL("../controller/web/dist/icons.svg", import.meta.url);
const files = (await readdir(source))
  .filter((name) => name.endsWith(".svg"))
  .sort((a, b) => a.localeCompare(b));

if (files.length === 0) {
  throw new Error("build-icons: no SVG sources; run controller/tools/fetch-assets.sh");
}

const symbols = [];
for (const file of files) {
  const svg = await readFile(join(source.pathname, file), "utf8");
  const match = svg.match(/<svg\b[^>]*>([\s\S]*?)<\/svg>\s*$/);
  if (!match) {
    throw new Error(`build-icons: malformed ${file}`);
  }
  const name = basename(file, ".svg");
  symbols.push(`<symbol id="i-${name}" viewBox="0 0 24 24">${match[1].trim()}</symbol>`);
}

await mkdir(new URL("../controller/web/dist/", import.meta.url), { recursive: true });
await writeFile(destination, `<svg xmlns="http://www.w3.org/2000/svg">${symbols.join("")}</svg>\n`);
