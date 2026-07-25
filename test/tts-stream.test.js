import assert from "node:assert/strict";
import test from "node:test";

import { createTtsStream } from "../controller/web/static/js/tts-stream.js";

function fakePlayer() {
  /** @type {string[]} */
  const events = [];
  /** @type {(() => void) | null} */
  let releaseBegin = null;
  return {
    events,
    async releaseBegin() {
      for (let spin = 0; spin < 20 && !releaseBegin; spin++) {
        await Promise.resolve();
      }
      if (releaseBegin) releaseBegin();
      releaseBegin = null;
    },
    /** @param {number} sampleRate */
    begin(sampleRate) {
      events.push(`begin:${sampleRate}`);
      return new Promise((resolve) => { releaseBegin = /** @type {() => void} */ (resolve); });
    },
    /** @param {string} pcm */
    push(pcm) {
      events.push(`push:${pcm}`);
    },
    stop() {
      events.push("stop");
      return Promise.resolve();
    },
    remainingMS() {
      return 5000;
    },
  };
}

test("chunks arriving during begin() are played after it, in order", async () => {
  const player = fakePlayer();
  const stream = createTtsStream({ player });
  stream.begin("req-1");
  const started = stream.frame({ type: "tts-start", id: "req-1", sampleRate: 22050, sentences: 2 });
  stream.frame({ type: "tts-chunk", id: "req-1", pcm: "AAA" });
  const last = stream.frame({ type: "tts-chunk", id: "req-1", pcm: "BBB" });
  // Nothing may be pushed while begin() is still pending.
  await Promise.resolve();
  assert.deepEqual(player.events, ["begin:22050"]);
  await player.releaseBegin();
  await started;
  await last;
  assert.deepEqual(player.events, ["begin:22050", "push:AAA", "push:BBB"]);
});

test("tts-error stops the player and clears active", async () => {
  const player = fakePlayer();
  /** @type {(string|null)[]} */
  const activeLog = [];
  const stream = createTtsStream({ player, onActiveChange: (id) => activeLog.push(id) });
  stream.begin("req-2");
  const start = stream.frame({ type: "tts-start", id: "req-2", sampleRate: 22050 });
  await player.releaseBegin();
  await start;
  await stream.frame({ type: "tts-error", id: "req-2", error: "boom" });
  assert.equal(stream.active, null);
  assert.deepEqual(activeLog, ["req-2", null]);
  assert.ok(player.events.includes("stop"));
});

test("tts-done clears active after playback drains", async () => {
  const player = fakePlayer();
  /** @type {{fn: () => void, ms: number}[]} */
  const timers = [];
  /** @type {any[]} */
  const seen = [];
  const stream = createTtsStream({
    player,
    onEvent: (message) => seen.push(message.type),
    schedule: (fn, ms) => { timers.push({ fn, ms }); return 0; },
  });
  stream.begin("req-3");
  const start = stream.frame({ type: "tts-start", id: "req-3", sampleRate: 22050 });
  await player.releaseBegin();
  await start;
  await stream.frame({ type: "tts-done", id: "req-3", audioSec: 3.2, rtf: 0.2 });
  assert.equal(stream.active, "req-3");
  assert.equal(timers.length, 1);
  assert.equal(timers[0].ms, 5000);
  timers[0].fn();
  assert.equal(stream.active, null);
  assert.ok(seen.includes("tts-finished"));
});

test("reset stops the player, clears active, and ignores stale frames", async () => {
  const player = fakePlayer();
  const stream = createTtsStream({ player });
  stream.begin("req-4");
  const start = stream.frame({ type: "tts-start", id: "req-4", sampleRate: 22050 });
  await player.releaseBegin();
  await start;
  await stream.reset();
  assert.equal(stream.active, null);
  await stream.frame({ type: "tts-chunk", id: "req-4", pcm: "ZZZ" });
  assert.equal(player.events.filter((event) => event.startsWith("push:")).length, 0);
});

test("frames for other request ids are ignored", async () => {
  const player = fakePlayer();
  const stream = createTtsStream({ player });
  stream.begin("mine");
  await stream.frame({ type: "tts-chunk", id: "other", pcm: "XXX" });
  assert.deepEqual(player.events, []);
});
