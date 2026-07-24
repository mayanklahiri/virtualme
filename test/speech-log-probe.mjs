// Verify persisted speech history after a container restart.
const url = process.argv[2];
if (!url) {
  console.error("usage: speech-log-probe.mjs <ws-url>");
  process.exit(2);
}

const socket = new WebSocket(url);
const timer = setTimeout(() => fail("timeout after 15s"), 15000);

/** @param {string} reason */
function fail(reason) {
  console.error(`speech-log-probe: FAIL: ${reason}`);
  process.exit(1);
}

socket.addEventListener("error", () => fail("websocket error"));
socket.addEventListener("open", () => {
  socket.send(JSON.stringify({ type: "speech-log-req" }));
});
socket.addEventListener("message", (event) => {
  let message;
  try { message = JSON.parse(String(event.data)); } catch { return; }
  if (message.type !== "speech-log") return;
  /** @type {Array<{origin?: string, voice?: string, cached?: boolean}>} */
  const entries = message.entries ?? [];
  if (!entries.some((entry) => entry.origin === "console")) fail("console history missing");
  if (!entries.some((entry) => entry.origin === "api")) fail("API history missing");
  if (!entries.some((entry) => entry.voice === "en_US-ryan-medium")) fail("Ryan history missing");
  if (!entries.some((entry) => entry.cached === true)) fail("cached history entry missing");
  clearTimeout(timer);
  console.log("speech-log-probe: OK");
  process.exit(0);
});
