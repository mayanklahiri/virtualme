// Real-stack browser-agent probe (Node >= 22, global WebSocket, zero deps).
const url = process.argv[2];
if (!url) {
  console.error("usage: agent-probe.mjs <ws-url>");
  process.exit(2);
}

const timeoutMs = Number(process.env.AGENT_E2E_TIMEOUT ?? 600) * 1000;
const timer = setTimeout(() => fail(`timeout after ${timeoutMs / 1000}s`), timeoutMs);
const socket = new WebSocket(url);
let taskId = "";
let sawStep = false;

/** @param {string} reason */
function fail(reason) {
  console.error(`agent-probe: FAIL: ${reason}`);
  process.exit(1);
}

socket.addEventListener("error", () => fail("websocket error"));
socket.addEventListener("close", () => fail("websocket closed early"));
socket.addEventListener("message", (event) => {
  let message;
  try {
    message = JSON.parse(String(event.data));
  } catch {
    return;
  }
  if (message.type === "chat-history") {
    socket.send(JSON.stringify({
      type: "chat",
      text: "Open https://example.com in the browser using the navigate tool, then read the page and tell me its title.",
    }));
  } else if (message.type === "agent-step") {
    sawStep = true;
    taskId = String(message.taskId ?? "");
    if (!["navigate", "screenshot", "read_page", "dom"].includes(message.tool)) {
      console.log(`agent-probe: observed additional tool ${message.tool}`);
    }
  } else if (message.type === "chat-error") {
    fail(`chat-error: ${message.error}`);
  } else if (message.type === "chat-done") {
    if (!sawStep || !taskId) fail("final reply arrived without an agent-step");
    if (!String(message.text ?? "").trim()) fail("final assistant reply is empty");
    clearTimeout(timer);
    console.log(`agent-probe: OK taskId=${taskId}`);
    process.exit(0);
  }
});
