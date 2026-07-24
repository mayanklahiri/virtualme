// Zero-dependency websocket probe for GPU-host E2E runs.
const url = process.argv[2];
if (!url) {
  console.error("usage: gpu-probe.mjs <ws-url>");
  process.exit(2);
}

/** @param {string} message */
const fail = (message) => {
  console.error(`gpu-probe: FAIL: ${message}`);
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
  if (message.gpu?.present !== true) {
    fail("state frame does not report a GPU");
  }
  clearTimeout(timer);
  console.log(`gpu-probe: OK (${message.gpu.vendor} ${message.gpu.model})`);
  process.exit(0);
});
