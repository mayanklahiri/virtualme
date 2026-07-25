import { readdir, readFile, stat } from "node:fs/promises";
import { dirname, extname, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";

const docs = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const dist = resolve(docs, "dist");
const expected = ["index.html", "guide/index.html", "architecture/index.html", "configuration/index.html", "blog/index.html", "blog/welcome/index.html", "about/index.html", "no-more-bills/index.html", "404.html"];
const errors = [];
async function walk(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = [];
  for (const entry of entries.sort((a, b) => Buffer.from(a.name).compare(Buffer.from(b.name)))) {
    const path = resolve(directory, entry.name);
    files.push(...(entry.isDirectory() ? await walk(path) : [path]));
  }
  return files;
}
const files = await walk(dist);
const names = files.map((file) => relative(dist, file).split(sep).join("/"));
for (const name of expected) if (!names.includes(name)) errors.push(`missing route: ${name}`);
if (!names.includes(".nojekyll")) errors.push("missing .nojekyll");
for (const name of names) if (/\.(?:ts|astro|md|mdx)$/.test(name) || /(?:^|\/)(?:package(?:-lock)?\.json|test)(?:\/|$)/.test(name)) errors.push(`source artifact deployed: ${name}`);

const htmlFiles = files.filter((file) => extname(file) === ".html");
for (const file of htmlFiles) {
  const html = await readFile(file, "utf8");
  const name = relative(dist, file).split(sep).join("/");
  if (Buffer.byteLength(html) > 256 * 1024) errors.push(`HTML exceeds 256 KiB: ${name}`);
  const count = (pattern) => (html.match(pattern) ?? []).length;
  for (const [label, pattern] of [["title", /<title(?:\s[^>]*)?>/g], ["description", /<meta name="description"/g], ["canonical", /<link rel="canonical"/g], ["h1", /<h1(?:\s|>)/g], ["main", /<main(?:\s|>)/g], ["footer", /<footer(?:\s|>)/g]]) if (count(pattern) !== 1) errors.push(`${name}: expected one ${label}`);
  if (!html.includes('© 2026 <a href="https://www.linkedin.com/in/mayanklahiri/">Mayank Lahiri</a>')) errors.push(`${name}: exact copyright missing`);
  const canonical = html.match(/<link rel="canonical" href="([^"]+)"/)?.[1];
  if (!canonical?.startsWith("https://mayanklahiri.github.io/virtualme/") || /localhost|\/docs\/|virtualme\/virtualme\//.test(canonical) || (canonical.endsWith(".html") && !canonical.endsWith("/404.html"))) errors.push(`${name}: invalid canonical ${canonical}`);
  const analyticsId = (process.env.PUBLIC_GA_MEASUREMENT_ID ?? "").trim();
  if (!analyticsId) {
    if (/googletagmanager|google-analytics|dataLayer|gtag\(/.test(html)) errors.push(`${name}: analytics emitted while disabled`);
  } else {
    if (count(/googletagmanager\.com\/gtag\/js\?id=G-TEST1234/g) !== 1 || count(/gtag\("config",\s*analyticsMeasurementId\)/g) !== 1) errors.push(`${name}: analytics count mismatch`);
  }
  const localReferences = [...html.matchAll(/\b(?:href|src)="([^"]+)"/g)].map((match) => match[1])
    .concat([...html.matchAll(/\bsrcset="([^"]+)"/g)].flatMap((match) => match[1].split(",").map((part) => part.trim().split(/\s+/)[0])));
  for (const match of html.matchAll(/\bsrc="(https?:\/\/[^"]+)"/g)) {
    if (!match[1].startsWith("https://www.googletagmanager.com/")) errors.push(`${name}: remote resource ${match[1]}`);
  }
  let pageJs = 0; let pageCss = 0;
  for (const reference of localReferences) {
    if (/^(?:https?:|mailto:|tel:|data:|#)/.test(reference)) {
      continue;
    }
    if (!reference.startsWith("/virtualme/")) { errors.push(`${name}: non-base local reference ${reference}`); continue; }
    const [path] = reference.slice("/virtualme/".length).split("#");
    const target = path === "" ? "index.html" : path.endsWith("/") ? `${path}index.html` : path;
    if (!names.includes(target)) errors.push(`${name}: missing target ${reference}`);
    else {
      const size = (await stat(resolve(dist, target))).size;
      if (target.endsWith(".js")) pageJs += size;
      if (target.endsWith(".css")) pageCss += size;
    }
  }
  if (pageJs > 96 * 1024) errors.push(`${name}: JavaScript budget exceeded`);
  if (pageCss > 160 * 1024) errors.push(`${name}: CSS budget exceeded`);
}

for (const file of files) {
  const name = relative(dist, file).split(sep).join("/");
  const info = await stat(file);
  if (/\.(?:png|jpe?g)$/i.test(name) && !/screenshots\//.test(name) && info.size > 500 * 1024) errors.push(`raster budget exceeded: ${name}`);
  if (name.endsWith(".map")) errors.push(`source map deployed: ${name}`);
}
if (errors.length) {
  errors.forEach((error) => console.error(`verify-build: ${error}`));
  process.exit(1);
}
console.log(`verify-build: OK (${htmlFiles.length} HTML files, ${files.length} total files)`);
