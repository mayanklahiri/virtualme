// Zero-dependency websocket probe for tiered metrics replies.
const requireNonEmpty = process.argv[2] === "--non-empty";
const url = requireNonEmpty ? process.argv[3] : process.argv[2];
if (!url) {
  console.error("usage: metrics-probe.mjs [--non-empty] <ws-url>");
  process.exit(2);
}

const wanted = new Map([["1h", 2], ["30d", 900]]);
const received = new Set();
const timer = setTimeout(() => {
  console.error("metrics-probe: FAIL: timeout after 30s");
  process.exit(1);
}, 30000);
const socket = new WebSocket(url);
socket.addEventListener("error", () => {
  console.error("metrics-probe: FAIL: websocket error");
  process.exit(1);
});
socket.addEventListener("open", () => {
  for (const lookback of wanted.keys()) {
    socket.send(JSON.stringify({ type: "metrics-req", lookback }));
  }
});
socket.addEventListener("message", (event) => {
  let message;
  try {
    message = JSON.parse(String(event.data));
  } catch {
    return;
  }
  if (message.type !== "metrics" || !wanted.has(message.lookback)) return;
  if (message.resSec !== wanted.get(message.lookback) || !Array.isArray(message.samples)) {
    console.error(`metrics-probe: FAIL: malformed ${message.lookback} reply`);
    process.exit(1);
  }
  if (requireNonEmpty && message.lookback === "1h" && message.samples.length === 0) {
    console.error("metrics-probe: FAIL: persisted 1h history is empty");
    process.exit(1);
  }
  received.add(message.lookback);
  if (received.size === wanted.size) {
    clearTimeout(timer);
    console.log("metrics-probe: OK");
    process.exit(0);
  }
});
