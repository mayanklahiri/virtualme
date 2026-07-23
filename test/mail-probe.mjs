// Real-stack outbound mail websocket probe.
const direct = process.argv[2] === "--direct";
const url = direct ? process.argv[3] : process.argv[2];
if (!url) {
  console.error("usage: mail-probe.mjs [--direct] <ws-url>");
  process.exit(2);
}

const socket = new WebSocket(url);
const id = direct ? "mail-direct" : "mail-relay";
const timer = setTimeout(() => fail("timeout after 60s"), 60000);
let accepted = false;

/** @param {string} reason */
function fail(reason) {
  console.error(`mail-probe: FAIL: ${reason}`);
  process.exit(1);
}

socket.addEventListener("error", () => fail("websocket error"));
socket.addEventListener("open", () => {
  socket.send(JSON.stringify({
    type: "mail-send", id,
    to: direct ? "nobody@invalid.test" : "recipient@example.test",
    subject: "Virtual Me outbound mail probe",
    body: "This is the plain text body.\n\nThe inline image follows.",
    includeTestImage: true,
  }));
});
socket.addEventListener("message", (event) => {
  let message;
  try { message = JSON.parse(String(event.data)); } catch { return; }
  if (message.type === "mail-result" && message.id === id) {
    if (!message.ok) fail(`submission failed: ${message.error}`);
    accepted = true;
    setTimeout(() => socket.send(JSON.stringify({ type: "mail-status-req" })), direct ? 2000 : 1000);
  } else if (accepted && message.type === "mail-status") {
    if (direct) {
      if (message.mode !== "direct" || !message.queue?.length) return;
    } else {
      if (message.mode !== "smarthost") fail(`mode is ${message.mode}`);
      if (!message.dkim?.enabled || !message.dkim.dnsName || !message.dkim.dnsValue) {
        fail("DKIM DNS record missing");
      }
      if (message.queue?.length) {
        setTimeout(() => socket.send(JSON.stringify({ type: "mail-status-req" })), 1000);
        return;
      }
    }
    clearTimeout(timer);
    console.log(`mail-probe: OK mode=${message.mode}`);
    process.exit(0);
  }
});
