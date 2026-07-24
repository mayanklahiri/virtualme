// Real-stack outbound mail websocket probe.
/** @typedef {{to?: string, subject?: string, preview?: string, lastError?: string, nextRetrySec?: number}} QueueEntry */
/** @typedef {{text?: string}} TimelineEntry */
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
let sawEnrichedQueue = false;
/** @type {ReturnType<typeof setTimeout> | undefined} */
let pollTimer;

/** @param {string} reason */
function fail(reason) {
  clearTimeout(pollTimer);
  console.error(`mail-probe: FAIL: ${reason}`);
  process.exit(1);
}

function poll() {
  clearTimeout(pollTimer);
  pollTimer = setTimeout(() => {
    socket.send(JSON.stringify({ type: "mail-status-req" }));
  }, 250);
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
  poll();
});
socket.addEventListener("message", (event) => {
  let message;
  try { message = JSON.parse(String(event.data)); } catch { return; }
  if (message.type === "mail-result" && message.id === id) {
    if (!message.ok) fail(`submission failed: ${message.error}`);
    accepted = true;
    poll();
  } else if (message.type === "mail-status") {
    const queue = /** @type {QueueEntry[]} */ (message.queue ?? []);
    const queued = queue.find((item) =>
      item.to?.includes(direct ? "nobody@invalid.test" : "recipient@example.test"));
    if (queued) {
      if (queued.subject !== "Virtual Me outbound mail probe" ||
          !queued.preview?.includes("This is the plain text body.")) {
        fail(`queue metadata incomplete: ${JSON.stringify(queued)}`);
      }
      sawEnrichedQueue = true;
    }
    if (!accepted) {
      poll();
      return;
    }
    if (direct) {
      if (message.mode !== "direct" || !sawEnrichedQueue || !queued ||
          !queued.lastError ||
          !Number.isFinite(queued.nextRetrySec)) {
        poll();
        return;
      }
    } else {
      if (message.mode !== "smarthost") fail(`mode is ${message.mode}`);
      if (!message.dkim?.enabled || !message.dkim.dnsName || !message.dkim.dnsValue) {
        fail("DKIM DNS record missing");
      }
      const timeline = /** @type {TimelineEntry[]} */ (message.timeline ?? []);
      if (!sawEnrichedQueue || queue.length ||
          !timeline.some((item) => item.text?.includes("left queue"))) {
        poll();
        return;
      }
    }
    clearTimeout(pollTimer);
    clearTimeout(timer);
    console.log(`mail-probe: OK mode=${message.mode}`);
    process.exit(0);
  }
});
