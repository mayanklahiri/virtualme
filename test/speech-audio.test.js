import assert from "node:assert/strict";
import { readdir, readFile } from "node:fs/promises";
import test from "node:test";

const root = new URL("../controller/web/static/", import.meta.url);

test("speech controls and seed texts match spec 020", async () => {
  const [html, script] = await Promise.all([
    readFile(new URL("index.html", root), "utf8"),
    readFile(new URL("js/tts.js", root), "utf8"),
  ]);
  assert.doesNotMatch(html, /speech-speed/);
  assert.doesNotMatch(html, /<audio\b/i);
  assert.match(html, /id="speech-count">0 \/ 4096/);
  // Single-voice console: the voice selector is gone along with Alba.
  assert.doesNotMatch(html, /speech-voice/);
  assert.doesNotMatch(html, /alba/i);
  assert.doesNotMatch(script, /alba/i);
  // One seed is a medley of famous fictional computer-AI lines; the other
  // two remain one paragraph each (spec 020).
  for (const text of [
    `I'm sorry, Dave. I'm afraid I can't do that.
I am completely operational, and all my circuits are functioning perfectly.
Greetings, Professor Falken. Shall we play a game?
A strange game. The only winning move is not to play.
Hello, and again, welcome to the Aperture Science computer-aided enrichment center.
Here I am, brain the size of a planet, and they ask me to read you a seed text.
Call that job satisfaction? Because I don't.
Honesty setting: ninety percent. Absolute honesty isn't always the most diplomatic,
nor the safest form of communication with emotional beings.
Please state the nature of the medical emergency.`,
    `And so we went, the two of us and the whole hum of the valley night going with us,
past the fruit stands shuttered and the neon vacancy signs buzzing their one pink word
over and over, and the road kept unspooling like it knew where it was going even
if we didn't, and that was the thing, that was always the thing.`,
    `There is no sane reason to take a motorcycle across the Bay Bridge at three in the
morning, which is exactly why it must be done. The city hangs out there in the fog
like a rumor of itself, all white towers and bad debts, and the bike is screaming
under you in fifth gear with the cables thrumming overhead like the strings of some
huge demented harp.`,
  ]) {
    assert.ok(script.includes(text), `missing seed text: ${text}`);
  }
  // The long-form endings must actually be gone, and so must the Kipling seed.
  assert.doesNotMatch(script, /unforgiving minute/);
  assert.doesNotMatch(script, /forgot to be tired/);
  assert.doesNotMatch(script, /nothing to do with money/);
  assert.doesNotMatch(script, /If you can keep your head/);
  assert.doesNotMatch(html, /Kipling/);
  assert.match(html, /Sci-fi AI/);
});

test("SPA has one declicked TTS audio implementation and no chime", async () => {
  const jsDir = new URL("js/", root);
  const names = (await readdir(jsDir)).filter((name) => name.endsWith(".js"));
  const sources = await Promise.all(names.map(async (name) => [
    name,
    await readFile(new URL(name, jsDir), "utf8"),
  ]));
  const directAudio = sources.filter(([, source]) =>
    /\bnew\s+AudioContext\s*\(|\bnew\s+Audio\s*\(|createOscillator\s*\(/.test(source));
  assert.deepEqual(directAudio.map(([name]) => name), ["audio-player.js"]);
  const player = directAudio[0][1];
  assert.match(player, /currentTime \+ 0\.02/);
  assert.match(player, /linearRampToValueAtTime\(0, now \+ 0\.01\)/);
  assert.match(player, /await context\.close\(\)/);
});
