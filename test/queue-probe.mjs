// Zero-dependency websocket probe for the reliable queue surface.
const url = process.argv[2];
if (!url) {
  console.error("usage: queue-probe.mjs <ws-url>");
  process.exit(2);
}

const echo = `queue-${Date.now()}`;
let id = "";
let runningSeen = false;
let finishedSeen = false;
const timer = setTimeout(() => {
  console.error("queue-probe: FAIL: timeout after 30s");
  process.exit(1);
}, 30000);
const socket = new WebSocket(url);
socket.addEventListener("error", () => {
  console.error("queue-probe: FAIL: websocket error");
  process.exit(1);
});
socket.addEventListener("open", () => {
  socket.send(JSON.stringify({
    type: "job-push",
    job: { type: "soak-probe", payload: { echo } },
  }));
});
socket.addEventListener("message", (event) => {
  let message;
  try {
    message = JSON.parse(String(event.data));
  } catch {
    return;
  }
  if (message.type === "job-pushed") {
    id = message.id;
    if (!id) {
      console.error("queue-probe: FAIL: job-pushed omitted id");
      process.exit(1);
    }
    socket.send(JSON.stringify({ type: "queue-peek" }));
    return;
  }
  if (message.type === "activity" && finishedSeen) {
    if (!Array.isArray(message.events)) {
      console.error("queue-probe: FAIL: activity reply omitted events array");
      process.exit(1);
    }
    clearTimeout(timer);
    console.log(`queue-probe: OK id=${id}`);
    process.exit(0);
  }
  if (message.type !== "queue-state" || !id) return;
  if (message.running?.id === id) runningSeen = true;
  let finished;
  for (const job of message.finished ?? []) {
    if (job.id === id) finished = job;
  }
  if (!finished) return;
  if (!runningSeen || finished.result?.ok !== true || finished.result?.summary !== echo) {
    console.error(`queue-probe: FAIL: invalid lifecycle ${JSON.stringify({ runningSeen, finished })}`);
    process.exit(1);
  }
  if (!finishedSeen) {
    finishedSeen = true;
    socket.send(JSON.stringify({ type: "activity-req" }));
  }
});
