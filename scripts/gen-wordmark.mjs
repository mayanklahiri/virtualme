// Regenerate controller/web/static/brand/wordmark.svg (committed output).
//
// The wordmark sets "VIRTUAL" in Archivo Black (block face, small-caps:
// all-caps letterforms at reduced cap height) and "me" in Caveat (casual
// handwritten face, slight italic skew) filled with the fixed brand red.
// Fonts are fetched from a pinned google/fonts commit, sha256-verified,
// converted to outlined paths with the exact-pinned opentype.js
// devDependency, and never shipped or loaded at runtime (constitution:
// zero runtime dependencies; spec 024 amendments).
//
// Usage: node scripts/gen-wordmark.mjs
import { Buffer } from "node:buffer";
import { createHash } from "node:crypto";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import opentype from "opentype.js";

const FONTS = [
  {
    file: "ArchivoBlack-Regular.ttf",
    url: "https://raw.githubusercontent.com/google/fonts/94a7d81318e438525a5285e07ab72c050fdfeb44/ofl/archivoblack/ArchivoBlack-Regular.ttf",
    sha256: "dd9a89a019b4849f66ab75455fe7bdf931311042cbb0f0f97acc061539703180",
  },
  {
    file: "Caveat.ttf",
    url: "https://raw.githubusercontent.com/google/fonts/5571d84c0d8c70ec1af4f64072d8c5cf1e4e9643/ofl/caveat/Caveat%5Bwght%5D.ttf",
    sha256: "0bdb6b660482d31531b3945849fba5916b3ef8695da7024a9e6b9ee3c4157988",
  },
];

const cacheDir = new URL("./.cache/wordmark-fonts/", import.meta.url);
const output = new URL("../controller/web/static/brand/wordmark.svg", import.meta.url);

const BRAND_RED = "#d63b2f";
// Layout constants (SVG user units). Tuned by eye against the sidebar chrome.
const CAP = 10.5; // small-caps height for "VIRTUAL" (blockier, subordinate)
const BASELINE = 24; // shared baseline for "me"
const ME_SIZE = 58; // Caveat renders small on the em square; oversize so "me" dominates
const ME_SKEW_DEG = -8; // gentle italic lean for "me"
const TRACKING = -0.03; // block-logo tightening (em) for "VIRTUAL"
const GAP = 2.5; // gap between the two words
const PAD = 1; // outer padding

function round(value) {
  return Math.round(value * 100) / 100;
}

async function loadFont({ file, url, sha256 }) {
  await mkdir(cacheDir, { recursive: true });
  const path = new URL(file, cacheDir);
  let bytes;
  try {
    bytes = await readFile(path);
  } catch {
    const response = await fetch(url);
    if (!response.ok) throw new Error(`gen-wordmark: fetch ${url}: ${response.status}`);
    bytes = Buffer.from(await response.arrayBuffer());
    await writeFile(path, bytes);
  }
  const digest = createHash("sha256").update(bytes).digest("hex");
  if (digest !== sha256) throw new Error(`gen-wordmark: sha256 mismatch for ${file}: ${digest}`);
  return opentype.parse(bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength));
}

/** Cap-height of a font in user units at a given font size. */
function capHeight(font, size) {
  const box = font.getPath("H", 0, 0, size, { kerning: false }).getBoundingBox();
  return box.y2 - box.y1;
}

/** Serialize an opentype path to a compact SVG "d" string. */
function toPathData(path) {
  return path.commands
    .map((c) => {
      switch (c.type) {
        case "M": return `M${round(c.x)} ${round(c.y)}`;
        case "L": return `L${round(c.x)} ${round(c.y)}`;
        case "C": return `C${round(c.x1)} ${round(c.y1)} ${round(c.x2)} ${round(c.y2)} ${round(c.x)} ${round(c.y)}`;
        case "Q": return `Q${round(c.x1)} ${round(c.y1)} ${round(c.x)} ${round(c.y)}`;
        case "Z": return "Z";
        default: throw new Error(`gen-wordmark: unknown command ${c.type}`);
      }
    })
    .join("");
}

const [archivo, caveat] = await Promise.all(FONTS.map(loadFont));

// "me" first: its ink box sets the vertical rhythm; Caveat sits small on the
// em square so ME_SIZE is tuned until the casual word clearly dominates.
const mePath = caveat.getPath("me", 0, BASELINE, ME_SIZE, { kerning: true });
const meBox = mePath.getBoundingBox();

// Size "VIRTUAL" so its cap height is exactly CAP (small-caps treatment), then
// optically center it against the taller "me" ink box.
const archivoSize = (CAP / capHeight(archivo, 100)) * 100;
const virtualProbe = archivo.getPath("VIRTUAL", 0, BASELINE, archivoSize, {
  kerning: true,
  letterSpacing: TRACKING,
});
const virtualProbeBox = virtualProbe.getBoundingBox();
const virtualBaseline =
  BASELINE + ((meBox.y1 + meBox.y2) - (virtualProbeBox.y1 + virtualProbeBox.y2)) / 2;
const virtualPath = archivo.getPath("VIRTUAL", 0, virtualBaseline, archivoSize, {
  kerning: true,
  letterSpacing: TRACKING,
});
const virtualBox = virtualPath.getBoundingBox();

const meX = virtualBox.x2 + GAP - meBox.x1;
const skew = Math.tan((ME_SKEW_DEG * Math.PI) / 180);
// skewX shifts points by y*tan(angle); compensate so the baseline stays put.
const meTransform = `translate(${round(meX - skew * BASELINE)} 0) skewX(${ME_SKEW_DEG})`;
const meRight = meX + meBox.x2 - meBox.x1 + Math.abs(skew) * (BASELINE - meBox.y1);

const top = Math.min(virtualBox.y1, meBox.y1) - PAD;
const bottom = Math.max(virtualBox.y2, meBox.y2) + PAD;
const width = round(meRight + PAD - (virtualBox.x1 - PAD));
const height = round(bottom - top);

const svg = `<svg xmlns="http://www.w3.org/2000/svg" class="wordmark-svg" viewBox="${round(virtualBox.x1 - PAD)} ${round(top)} ${width} ${height}">
  <path fill="var(--wordmark-fill, #7d8590)" d="${toPathData(virtualPath)}"/>
  <path fill="${BRAND_RED}" transform="${meTransform}" d="${toPathData(mePath)}"/>
</svg>
`;

await writeFile(output, svg);
console.log(`gen-wordmark: wrote ${output.pathname} (viewBox ${round(virtualBox.x1 - PAD)} ${round(top)} ${width} ${height})`);
