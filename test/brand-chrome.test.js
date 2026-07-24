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
  assert.match(html, /class="status-band"/);
  assert.match(html, /id="home-address"/);
  assert.match(html, /id="home-version">Virtual Me</);
  assert.match(css, /\.quick-links\{grid-template-columns:1fr\}/);
  assert.match(css, /prefers-reduced-motion:reduce/);
  assert.match(css, /\[data-theme=contrast\] \.wordmark-svg/);
  assert.match(conn, /requestAnimationFrame/);
  assert.match(render, /snapshot\.services\.length/);
  assert.doesNotMatch(wordmark, /<text\b/);
  assert.match(wordmark, /wordmark-m-slice/);
});

test("SPA-visible copy has no banned phrases or em dashes", async () => {
  const files = [
    new URL("index.html", root),
    ...["agent", "app", "audio-player", "chart", "chat", "conn", "jobs", "mail", "markdown", "nav", "projects", "render", "router", "theme", "tools", "tts", "ws"]
      .map((name) => new URL(`js/${name}.js`, root)),
  ];
  const copy = (await Promise.all(files.map((file) => readFile(file, "utf8")))).join("\n");
  assert.doesNotMatch(copy, /—/);
  for (const phrase of banned) {
    assert.equal(copy.toLowerCase().includes(phrase.toLowerCase()), false, phrase);
  }
});
