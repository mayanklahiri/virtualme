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
  assert.match(html, /value="en_US-lessac-medium"/);
  assert.match(html, /value="en_US-ryan-medium"/);
  for (const text of [
    `If you can keep your head when all about you
Are losing theirs and blaming it on you,
If you can trust yourself when all men doubt you,
But make allowance for their doubting too;
If you can wait and not be tired by waiting,
Or being lied about, don't deal in lies,
Or being hated, don't give way to hating,
And yet don't look too good, nor talk too wise.

If you can fill the unforgiving minute
With sixty seconds' worth of distance run,
Yours is the Earth and everything that's in it,
And, which is more, you'll be a Man, my son!`,
    `And so we went, the two of us and the whole hum of the valley night going with us,
past the fruit stands shuttered and the neon vacancy signs buzzing their one pink word
over and over, and I thought about everybody asleep in all those rooms dreaming their
separate Americas, and the road kept unspooling like it knew where it was going even
if we didn't, and that was the thing, that was always the thing. You don't drive the
road, the road drives you, mile after holy mile, until the dawn comes up gray and
gorgeous over the flats and you remember you forgot to be tired.`,
    `There is no sane reason to take a motorcycle across the Bay Bridge at three in the
morning, which is exactly why it must be done. The city hangs out there in the fog
like a rumor of itself, all white towers and bad debts, and the bike is screaming
under you in fifth gear with the cables thrumming overhead like the strings of some
huge demented harp. Halfway across you stop being a person with obligations and
become a simple physics problem: velocity, wind, nerve, and the toll you pay on
the far side has nothing to do with money.`,
  ]) {
    assert.ok(script.includes(text), `missing seed text: ${text}`);
  }
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
