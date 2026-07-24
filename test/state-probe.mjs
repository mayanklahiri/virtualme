// Zero-dependency websocket probe for homepage state fields.
const url = process.argv[2];
if (!url) {
  console.error("usage: state-probe.mjs <ws-url>");
  process.exit(2);
}

/** @param {string} message */
const fail = (message) => {
  console.error(`state-probe: FAIL: ${message}`);
  process.exit(1);
};
const timer = setTimeout(() => fail("timeout after 30s"), 30000);
const socket = new WebSocket(url);
socket.addEventListener("error", () => fail("websocket error"));
socket.addEventListener("message", (event) => {
  let message;
  try {
    message = JSON.parse(String(event.data));
  } catch {
    return;
  }
  if (message.type !== "state") return;
  if (typeof message.hostname !== "string" || message.hostname.length === 0) {
    fail("state frame missing hostname");
  }
  if (!Number.isFinite(message.system?.diskTotalMB) || message.system.diskTotalMB <= 0) {
    fail("state frame missing diskTotalMB");
  }
  if (!Date.parse(message.scheduler?.localTime) || typeof message.scheduler?.tz !== "string" ||
      !Array.isArray(message.scheduler?.active) || message.scheduler.active.length < 5) {
    fail("state frame missing scheduler selectors");
  }
  if (typeof message.jiggler?.enabled !== "boolean") {
    fail("state frame missing jiggler enabled state");
  }
  clearTimeout(timer);
  console.log("state-probe: OK");
  process.exit(0);
});
