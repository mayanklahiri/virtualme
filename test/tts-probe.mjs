// Real-stack local TTS websocket and OpenAI-compatible API probe.
import { Buffer } from "node:buffer";

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

async function checkHTTP() {
  const response = await fetch(`${base}/v1/audio/speech`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ input: "Hello from the speech API." }),
  });
  const body = new Uint8Array(await response.arrayBuffer());
  if (!response.ok) fail(`speech API returned ${response.status}`);
  if (!String(response.headers.get("content-type")).startsWith("audio/wav")) fail("speech API content type");
  if (body[0] !== 82 || body[1] !== 73 || body[2] !== 70 || body[3] !== 70) fail("speech API missing RIFF");
  if (body.length <= 10000) fail(`speech API body too short: ${body.length}`);
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
      const view = new DataView(raw.buffer);
      let sum = 0;
      for (let index = 0; index + 1 < raw.length; index += 2) {
        const sample = view.getInt16(index, true);
        sum += sample * sample;
      }
      const rms = Math.sqrt(sum / Math.max(1, raw.length / 2));
      if (rms < 10) fail(`silent PCM chunk: RMS ${rms}`);
    } else if (message.type === "tts-done") {
      if (chunks < 2) fail(`only ${chunks} chunks`);
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
