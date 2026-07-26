import test from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";

const root = new URL("../controller/web/static/", import.meta.url);
const banned = [
  "so you don't have to", "supercharge", "seamless", "effortless", "unleash",
  "empower", "delve", "elevate", "game-changing", "AI-powered",
];

test("brand chrome markup and responsive contracts stay intact", async () => {
  const [html, css, conn, render, wordmark] = await Promise.all([
    readFile(new URL("index.html", root), "utf8"),
    readFile(new URL("css/app.css", root), "utf8"),
    readFile(new URL("js/conn.js", root), "utf8"),
    readFile(new URL("js/render.js", root), "utf8"),
    readFile(new URL("brand/wordmark.svg", root), "utf8"),
  ]);
  assert.equal((html.match(/data-connection/g) ?? []).length, 2);
  // Spec 026 U4: the wristwatch dial is gone; a simple status pip remains.
  assert.doesNotMatch(html, /conn-dial|dial-hand|dial-ticks|dial-uptime/);
  assert.match(html, /conn-pip/);
  assert.doesNotMatch(conn, /requestAnimationFrame|dial/);
  assert.match(html, /class="status-band"/);
  assert.match(html, /id="home-address"/);
  assert.match(html, /id="home-version">Virtual Me</);
  assert.match(css, /\.quick-links\{grid-template-columns:1fr\}/);
  assert.match(css, /prefers-reduced-motion:reduce/);
  assert.match(css, /\[data-theme=contrast\] \.wordmark-svg/);
  assert.doesNotMatch(html, /class="brand-mark"/);
  assert.match(css, /\.wordmark-svg\{width:12\.5rem;max-width:100%;height:auto;/);
  assert.match(css, /\.brand\{[^}]*overflow:visible/);
  assert.match(css, /main\{[^}]*overflow:clip/);
  assert.doesNotMatch(css, /main\{[^}]*overflow:hidden/);
  assert.match(css, /\.page-heading\{position:sticky;top:0;z-index:15/);
  assert.match(css, /#status-summary\{position:sticky;top:0;z-index:15/);
  assert.match(css, /@media\(max-width:43\.75rem\)\{\.page-caption\{display:none\}\}/);
  assert.match(css, /--page-pad:clamp\(1\.25rem,3vw,2\.25rem\)/);
  for (const caption of [
    "Queue, schedule, and recent machine activity.",
    "Talk to the local model; browser tasks show each step.",
    "Optional Telegram bridge over outbound long polling.",
    "Local text-to-speech with playback history.",
    "Compose and track outbound mail with DKIM signing.",
    "Watch the virtual desktop live.",
  ]) {
    assert.ok(html.includes(caption), `missing page caption: ${caption}`);
  }
  assert.match(html, /page-heading-actions"><button id="notifications-read-all"/);
  const homeStart = html.indexOf('data-page="home"');
  assert.doesNotMatch(html.slice(homeStart, html.indexOf("</section>", homeStart)), /page-heading/);
  assert.match(wordmark, /viewBox="-0\.74 0\.7 128\.5 23\.53"/);
  assert.match(render, /snapshot\.services\.length/);
  assert.doesNotMatch(wordmark, /<text\b/);
  assert.match(wordmark, /var\(--wordmark-fill/);
  assert.match(wordmark, /#d63b2f/);
});

test("SPA-visible copy has no banned phrases or em dashes", async () => {
  const files = [
    new URL("index.html", root),
    ...["agent", "app", "audio-player", "chart", "chat", "conn", "jobs", "mail", "markdown", "markdown-table", "nav", "projects", "render", "router", "theme", "tools", "tts", "tts-stream", "ws"]
      .map((name) => new URL(`js/${name}.js`, root)),
  ];
  const copy = (await Promise.all(files.map((file) => readFile(file, "utf8")))).join("\n");
  assert.doesNotMatch(copy, /—/);
  for (const phrase of banned) {
    assert.equal(copy.toLowerCase().includes(phrase.toLowerCase()), false, phrase);
  }
});
