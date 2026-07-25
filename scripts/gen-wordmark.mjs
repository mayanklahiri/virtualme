// Regenerate controller/web/static/brand/wordmark.svg (committed output).
//
// The wordmark sets "Virtual" and "me" in Chakra Petch Bold (squared,
// technical, clean-lined face; spec 026 U3), with "me" filled in the fixed
// brand red and scaled so its lowercase body reads balanced against the
// capitals of "Virtual". The font is fetched from a pinned google/fonts
// commit, sha256-verified, converted to outlined paths with the exact-pinned
// opentype.js devDependency, and never shipped or loaded at runtime
// (constitution: zero runtime dependencies; spec 024 amendments).
//
// Usage: node scripts/gen-wordmark.mjs
import { Buffer } from "node:buffer";
import { createHash } from "node:crypto";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import opentype from "opentype.js";

const FONT = {
  file: "ChakraPetch-Bold.ttf",
  url: "https://raw.githubusercontent.com/google/fonts/94a7d81318e438525a5285e07ab72c050fdfeb44/ofl/chakrapetch/ChakraPetch-Bold.ttf",
  sha256: "65fbf76d95651697275e19db4d717c0e95a789ddd3476478b05292104db278a0",
};

const cacheDir = new URL("./.cache/wordmark-fonts/", import.meta.url);
const output = new URL("../controller/web/static/brand/wordmark.svg", import.meta.url);

const BRAND_RED = "#d63b2f";
// Layout constants (SVG user units). Tuned by eye against the sidebar chrome.
const CAP = 20; // cap height for "Virtual"
const BASELINE = 24; // shared baseline
const ME_XHEIGHT_RATIO = 0.82; // "me" x-height relative to CAP (balanced, not shrunken)
const TRACKING = -0.01; // slight block-logo tightening (em)
const GAP = 2.2; // gap between the two words
const PAD = 1; // outer padding

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

const chakra = await loadFont(FONT);

/** Height of a glyph's ink box at a given font size. */
function inkHeight(font, glyph, size) {
  const box = font.getPath(glyph, 0, 0, size, { kerning: false }).getBoundingBox();
  return box.y2 - box.y1;
}

// Size "Virtual" so its cap height is exactly CAP.
const virtualSize = (CAP / inkHeight(chakra, "H", 100)) * 100;
const virtualPath = chakra.getPath("Virtual", 0, BASELINE, virtualSize, {
  kerning: true,
  letterSpacing: TRACKING,
});
const virtualBox = virtualPath.getBoundingBox();

// Size "me" from its x-height so the lowercase body holds its own next to
// the capitals instead of reading like an afterthought.
const meSize = ((CAP * ME_XHEIGHT_RATIO) / inkHeight(chakra, "x", 100)) * 100;
const meRaw = chakra.getPath("me", 0, BASELINE, meSize, { kerning: true, letterSpacing: TRACKING });
const meRawBox = meRaw.getBoundingBox();
const meX = virtualBox.x2 + GAP - meRawBox.x1;
const mePath = chakra.getPath("me", meX, BASELINE, meSize, { kerning: true, letterSpacing: TRACKING });
const meBox = mePath.getBoundingBox();

const top = Math.min(virtualBox.y1, meBox.y1) - PAD;
const bottom = Math.max(virtualBox.y2, meBox.y2) + PAD;
const width = round(meBox.x2 + PAD - (virtualBox.x1 - PAD));
const height = round(bottom - top);

function round(value) {
  return Math.round(value * 100) / 100;
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

const svg = `<svg xmlns="http://www.w3.org/2000/svg" class="wordmark-svg" viewBox="${round(virtualBox.x1 - PAD)} ${round(top)} ${width} ${height}">
  <path fill="var(--wordmark-fill, #7d8590)" d="${toPathData(virtualPath)}"/>
  <path fill="${BRAND_RED}" d="${toPathData(mePath)}"/>
</svg>
`;

await writeFile(output, svg);
console.log(`gen-wordmark: wrote ${output.pathname} (viewBox ${round(virtualBox.x1 - PAD)} ${round(top)} ${width} ${height})`);
