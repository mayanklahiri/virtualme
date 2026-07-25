// Real-stack local TTS websocket and OpenAI-compatible API probe.
import { Buffer } from "node:buffer";
import { performance } from "node:perf_hooks";

const url = process.argv[2];
const base = process.argv[3] ?? "http://127.0.0.1:8080";
if (!url) {
  console.error("usage: tts-probe.mjs <ws-url> [http-base]");
  process.exit(2);
}

const socket = new WebSocket(url);
const timer = setTimeout(() => fail("timeout after 120s"), 120000);
let phase = "normal";
let chunks = 0;
let lastSeq = -1;
let stoppedDone = false;
let phaseStarted = 0;
let firstDuration = 0;
let lessacPCM = "";
let fallbackPCM = "";
const cacheText = "A unique sentence verifies the exact speech cache.";
const voiceText = "Unknown voices must fall back to the sole baked voice.";

/** @param {string} reason */
function fail(reason) {
  console.error(`tts-probe: FAIL: ${reason}`);
  process.exit(1);
}

function sendNormal() {
  socket.send(JSON.stringify({
    type: "tts-req", id: "probe-normal",
    text: "Hello from Virtual Me. This is a test.", speed: 1,
  }));
}

/** @param {string} id @param {string} text @param {string} voice */
function request(id, text, voice) {
  phaseStarted = performance.now();
  chunks = 0;
  lastSeq = -1;
  socket.send(JSON.stringify({ type: "tts-req", id, text, voice }));
}

// A removed/unknown voice must fall back to the sole baked voice.
async function checkHTTP(voice = "en_GB-alba-medium") {
  const response = await fetch(`${base}/v1/audio/speech`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ input: "Hello from the speech API.", voice }),
  });
  const body = new Uint8Array(await response.arrayBuffer());
  if (!response.ok) fail(`speech API returned ${response.status}`);
  if (!String(response.headers.get("content-type")).startsWith("audio/wav")) fail("speech API content type");
  if (body[0] !== 82 || body[1] !== 73 || body[2] !== 70 || body[3] !== 70) fail("speech API missing RIFF");
  if (body.length <= 10000) fail(`speech API body too short: ${body.length}`);
  if (response.headers.get("x-vm-voice") !== "en_US-lessac-medium") fail("speech API voice fallback");
}

socket.addEventListener("error", () => fail("websocket error"));
socket.addEventListener("open", sendNormal);
socket.addEventListener("message", async (event) => {
  let message;
  try { message = JSON.parse(String(event.data)); } catch { return; }
  if (message.origin !== "console") return;
  if (phase === "normal" && message.id === "probe-normal") {
    if (message.type === "tts-start") {
      if (!(message.sampleRate > 0) || message.sentences !== 2) fail("malformed start frame");
    } else if (message.type === "tts-chunk") {
      if (message.seq !== lastSeq + 1) fail("unordered chunks");
      lastSeq = message.seq;
      chunks += 1;
      const raw = Buffer.from(message.pcm, "base64");
      const view = new DataView(raw.buffer, raw.byteOffset, raw.byteLength);
      let sum = 0;
      for (let index = 0; index + 1 < raw.length; index += 2) {
        const sample = view.getInt16(index, true);
        sum += sample * sample;
      }
      const rms = Math.sqrt(sum / Math.max(1, raw.length / 2));
      if (rms < 10) fail(`silent PCM chunk: RMS ${rms}`);
    } else if (message.type === "tts-done") {
      if (chunks < 2) fail(`only ${chunks} chunks`);
      phase = "cache-first";
      request("probe-cache-first", cacheText, "en_US-lessac-medium");
    }
  } else if (phase === "cache-first" && message.id === "probe-cache-first" && message.type === "tts-done") {
    firstDuration = performance.now() - phaseStarted;
    if (message.cached) fail("first cache request unexpectedly hit");
    phase = "cache-second";
    request("probe-cache-second", cacheText, "en_US-lessac-medium");
  } else if (phase === "cache-second" && message.id === "probe-cache-second" && message.type === "tts-done") {
    const secondDuration = performance.now() - phaseStarted;
    if (!message.cached) fail("second cache request missed");
    if (secondDuration >= firstDuration * 0.25) {
      fail(`cache speedup insufficient: ${secondDuration.toFixed(0)}ms vs ${firstDuration.toFixed(0)}ms`);
    }
    phase = "voice-lessac";
    request("probe-voice-lessac", voiceText, "en_US-lessac-medium");
  } else if (phase === "voice-lessac" && message.id === "probe-voice-lessac") {
    if (message.type === "tts-chunk") lessacPCM += message.pcm;
    if (message.type === "tts-done") {
      phase = "voice-fallback";
      request("probe-voice-fallback", voiceText, "en_GB-alba-medium");
    }
  } else if (phase === "voice-fallback" && message.id === "probe-voice-fallback") {
    if (message.type === "tts-chunk") fallbackPCM += message.pcm;
    if (message.type === "tts-done") {
      if (!fallbackPCM || fallbackPCM !== lessacPCM) fail("removed-voice request did not fall back to the Lessac render");
      try {
        await checkHTTP();
      } catch (error) {
        fail(error instanceof Error ? error.message : String(error));
      }
      phase = "stop";
      socket.send(JSON.stringify({
        type: "tts-req", id: "probe-stop",
        text: `${"This deliberately long sentence keeps synthesis active for a cancellation test. ".repeat(40)} Final sentence.`,
      }));
    }
  } else if (phase === "stop" && message.id === "probe-stop") {
    if (message.type === "tts-start" || message.type === "tts-chunk") {
      socket.send(JSON.stringify({ type: "tts-stop", id: "probe-stop" }));
    } else if (message.type === "tts-done") {
      stoppedDone = true;
    } else if (message.type === "tts-status" && message.phase === "stopped") {
      setTimeout(() => {
        if (stoppedDone) fail("tts-stop was followed by tts-done");
        clearTimeout(timer);
        console.log("tts-probe: OK");
        process.exit(0);
      }, 1000);
    }
  }
});
