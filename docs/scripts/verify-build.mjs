import { readdir, readFile, stat } from "node:fs/promises";
import { dirname, extname, posix, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";

const docs = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const dist = resolve(docs, "dist");
const expected = ["index.html", "guide/index.html", "architecture/index.html", "configuration/index.html", "blog/index.html", "blog/welcome/index.html", "about/index.html", "no-more-bills/index.html", "404.html"];
const errors = [];
const base = "/virtualme/";
const origin = "https://mayanklahiri.github.io";
const siteSource = await readFile(resolve(docs, "src/config/site.ts"), "utf8");
const committedAnalyticsId = siteSource.match(/const committedAnalyticsMeasurementId = "([^"]*)"/)?.[1];
if (committedAnalyticsId === undefined) throw new Error("cannot read committed analytics ID");
const analyticsId = (process.env.PUBLIC_GA_MEASUREMENT_ID ?? "").trim() || committedAnalyticsId;
if (analyticsId && !/^G-[A-Z0-9]+$/.test(analyticsId)) errors.push("analytics ID must match ^G-[A-Z0-9]+$");
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
const nameSet = new Set(names);
for (const name of expected) if (!names.includes(name)) errors.push(`missing route: ${name}`);
if (!names.includes(".nojekyll")) errors.push("missing .nojekyll");
for (const name of names) if (/\.(?:ts|astro|md|mdx)$/.test(name) || /(?:^|\/)(?:package(?:-lock)?\.json|test)(?:\/|$)/.test(name)) errors.push(`source artifact deployed: ${name}`);

const htmlFiles = files.filter((file) => extname(file) === ".html");
const htmlByName = new Map(await Promise.all(htmlFiles.map(async (file) => {
  const name = relative(dist, file).split(sep).join("/");
  return [name, await readFile(file, "utf8")];
})));
const idsByName = new Map([...htmlByName].map(([name, html]) => [
  name,
  new Set([...html.matchAll(/\bid="([^"]+)"/g)].map((match) => match[1])),
]));
const referenced = new Set();
const graphQueue = [];
const screenshotEntries = await readdir(resolve(docs, "src/screenshots"), { withFileTypes: true });
const screenshotLimits = new Map(await Promise.all(screenshotEntries
  .filter((entry) => entry.isFile() && /\.(?:png|jpe?g)$/i.test(entry.name))
  .map(async (entry) => [entry.name.replace(/\.[^.]+$/, ""), (await stat(resolve(docs, "src/screenshots", entry.name))).size])));
const screenshotStem = (name) => [...screenshotLimits.keys()].find((stem) => posix.basename(name).startsWith(`${stem}.`) || posix.basename(name) === stem);
const localTarget = (reference) => {
  if (!reference.startsWith(base)) return undefined;
  const raw = reference.slice(base.length).split("?")[0].split("#")[0];
  return raw === "" ? "index.html" : raw.endsWith("/") ? `${raw}index.html` : raw;
};
const fragmentTarget = (reference, currentName) => {
  const hash = reference.indexOf("#");
  if (hash < 0 || hash === reference.length - 1) return undefined;
  const fragment = decodeURIComponent(reference.slice(hash + 1));
  const target = reference.startsWith("#") ? currentName : localTarget(reference);
  return target ? { target, fragment } : undefined;
};
const addReference = (reference, currentName) => {
  const target = localTarget(reference);
  if (target) {
    referenced.add(target);
    if (nameSet.has(target) && !graphQueue.includes(target)) graphQueue.push(target);
  }
  const fragment = fragmentTarget(reference, currentName);
  if (fragment && (!idsByName.has(fragment.target) || !idsByName.get(fragment.target).has(fragment.fragment))) {
    errors.push(`${currentName}: missing fragment ${reference}`);
  }
  return target;
};
for (const file of htmlFiles) {
  const name = relative(dist, file).split(sep).join("/");
  const html = htmlByName.get(name);
  if (Buffer.byteLength(html) > 256 * 1024) errors.push(`HTML exceeds 256 KiB: ${name}`);
  const count = (pattern) => (html.match(pattern) ?? []).length;
  for (const [label, pattern] of [["title", /<title(?:\s[^>]*)?>/g], ["description", /<meta name="description"/g], ["canonical", /<link rel="canonical"/g], ["h1", /<h1(?:\s|>)/g], ["main", /<main(?:\s|>)/g], ["footer", /<footer(?:\s|>)/g]]) if (count(pattern) !== 1) errors.push(`${name}: expected one ${label}`);
  if (!html.includes('© 2026 <a href="https://www.linkedin.com/in/mayanklahiri/">Mayank Lahiri</a>')) errors.push(`${name}: exact copyright missing`);
  const canonical = html.match(/<link rel="canonical" href="([^"]+)"/)?.[1];
  const expectedCanonical = name === "404.html"
    ? `${origin}${base}404.html`
    : `${origin}${base}${name === "index.html" ? "" : name.replace(/index\.html$/, "")}`;
  if (canonical !== expectedCanonical) errors.push(`${name}: invalid canonical ${canonical}; expected ${expectedCanonical}`);
  if (!analyticsId) {
    if (/googletagmanager|google-analytics|dataLayer|gtag\(/.test(html)) errors.push(`${name}: analytics emitted while disabled`);
  } else {
    const escaped = analyticsId.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
    if (count(new RegExp(`googletagmanager\\.com/gtag/js\\?id=${escaped}`, "g")) !== 1 || count(/gtag\("config",\s*analyticsMeasurementId\)/g) !== 1) errors.push(`${name}: analytics count mismatch`);
  }
  const pageAssets = new Set();
  for (const tagMatch of html.matchAll(/<([a-z0-9-]+)\b([^>]*)>/gi)) {
    const tag = tagMatch[1].toLowerCase();
    const attributes = Object.fromEntries([...tagMatch[2].matchAll(/\b([a-z0-9:-]+)="([^"]*)"/gi)].map((match) => [match[1].toLowerCase(), match[2]]));
    const references = [];
    if (attributes.href) references.push(attributes.href);
    if (attributes.src) references.push(attributes.src);
    if (attributes.srcset) references.push(...attributes.srcset.split(",").map((part) => part.trim().split(/\s+/)[0]));
    for (const reference of references) {
      if (/^https?:\/\//.test(reference)) {
        const analyticsLoader = tag === "script" && reference === `https://www.googletagmanager.com/gtag/js?id=${analyticsId}`;
        const canonicalLink = tag === "link" && attributes.rel === "canonical";
        if (tag !== "a" && !analyticsLoader && !canonicalLink) errors.push(`${name}: remote resource ${reference}`);
        continue;
      }
      if (/^(?:mailto:|tel:|data:)/.test(reference)) continue;
      if (reference.startsWith("#")) { addReference(reference, name); continue; }
      if (!reference.startsWith(base)) { errors.push(`${name}: non-base local reference ${reference}`); continue; }
      const target = addReference(reference, name);
      if (!target || !nameSet.has(target)) errors.push(`${name}: missing target ${reference}`);
      else pageAssets.add(target);
    }
    if (tag === "img") {
      const imageTargets = references.map(localTarget).filter(Boolean);
      const screenshot = imageTargets.find((target) => screenshotStem(target));
      if (screenshot && attributes.loading !== "lazy") errors.push(`${name}: screenshot must lazy-load: ${screenshot}`);
      if (screenshot && (!attributes.width || !attributes.height)) errors.push(`${name}: screenshot dimensions missing: ${screenshot}`);
    }
  }
  let pageJs = 0; let pageCss = 0;
  for (const target of pageAssets) {
    const size = (await stat(resolve(dist, target))).size;
    if (target.endsWith(".js")) pageJs += size;
    if (target.endsWith(".css")) pageCss += size;
  }
  if (pageJs > 96 * 1024) errors.push(`${name}: JavaScript budget exceeded`);
  if (pageCss > 160 * 1024) errors.push(`${name}: CSS budget exceeded`);
}

while (graphQueue.length) {
  const name = graphQueue.shift();
  if (!/\.(?:css|js)$/i.test(name)) continue;
  const text = await readFile(resolve(dist, name), "utf8");
  const candidates = name.endsWith(".css")
    ? [...text.matchAll(/url\((?:["']?)([^"')]+)(?:["']?)\)/g)].map((match) => match[1])
    : [...text.matchAll(/(?:import\s*(?:\(\s*)?|from\s*)["']([^"']+)["']/g)].map((match) => match[1]);
  for (const candidate of candidates) {
    if (/^(?:data:|https?:)/.test(candidate)) {
      if (/^https?:/.test(candidate)) errors.push(`${name}: remote bundled resource ${candidate}`);
      continue;
    }
    const target = candidate.startsWith(base)
      ? localTarget(candidate)
      : posix.normalize(posix.join(posix.dirname(name), candidate.split("?")[0].split("#")[0]));
    if (!target || !nameSet.has(target)) errors.push(`${name}: missing bundled target ${candidate}`);
    else if (!referenced.has(target)) {
      referenced.add(target);
      graphQueue.push(target);
    }
  }
}

for (const file of files) {
  const name = relative(dist, file).split(sep).join("/");
  const info = await stat(file);
  const stem = screenshotStem(name);
  if (/\.(?:png|jpe?g)$/i.test(name) && stem && info.size > screenshotLimits.get(stem)) errors.push(`screenshot budget exceeded: ${name}`);
  if (/\.(?:png|jpe?g)$/i.test(name) && !stem && info.size > 500 * 1024) errors.push(`raster budget exceeded: ${name}`);
  if (name.startsWith("_astro/") && !referenced.has(name)) errors.push(`unreferenced build asset: ${name}`);
  if (name.endsWith(".map")) errors.push(`source map deployed: ${name}`);
}
if (errors.length) {
  errors.forEach((error) => console.error(`verify-build: ${error}`));
  process.exit(1);
}
console.log(`verify-build: OK (${htmlFiles.length} HTML files, ${files.length} total files)`);
