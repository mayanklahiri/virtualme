// Zero-dependency websocket probe for persisted jiggler state.
const args = process.argv.slice(2);
const expectOnly = args[0] === "--expect";
const desiredText = expectOnly ? args[1] : args[0];
const url = expectOnly ? args[2] : args[1];
if (!["true", "false"].includes(desiredText) || !url) {
  console.error("usage: jiggler-probe.mjs [--expect] <true|false> <ws-url>");
  process.exit(2);
}
const desired = desiredText === "true";
/** @param {string} message */
const fail = (message) => {
  console.error(`jiggler-probe: FAIL: ${message}`);
  process.exit(1);
};
const timer = setTimeout(() => fail("timeout after 15s"), 15000);
const socket = new WebSocket(url);
let sent = expectOnly;
socket.addEventListener("error", () => fail("websocket error"));
socket.addEventListener("open", () => {
  if (!expectOnly) {
    socket.send(JSON.stringify({ type: "jiggler-set", enabled: desired }));
    sent = true;
  }
});
socket.addEventListener("message", (event) => {
  let message;
  try {
    message = JSON.parse(String(event.data));
  } catch {
    return;
  }
  if (!sent || message.type !== "state" || message.jiggler?.enabled !== desired) return;
  clearTimeout(timer);
  console.log(`jiggler-probe: OK enabled=${desired}`);
  socket.close();
});
