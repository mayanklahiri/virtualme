// Chat probe for test/e2e.sh (Node >= 22, global WebSocket, zero deps).
//
//   node test/chat-probe.mjs <ws-url>                 exit 0 on the first
//     chat-delta after sending a one-word prompt (timeout CHAT_PROBE_TIMEOUT,
//     default 240 s).
//   node test/chat-probe.mjs --history-only <ws-url>  exit 0 iff the
//     chat-history received on connect contains at least one message.

const historyOnly = process.argv[2] === "--history-only";
const url = historyOnly ? process.argv[3] : process.argv[2];
if (!url) {
  console.error("usage: chat-probe.mjs [--history-only] <ws-url>");
  process.exit(2);
}
const timeoutMs = Number(process.env.CHAT_PROBE_TIMEOUT ?? 240) * 1000;

/** @param {string} reason */
function bail(reason) {
  console.error(`chat-probe: FAIL: ${reason}`);
  process.exit(1);
}

const timer = setTimeout(() => bail(`timeout after ${timeoutMs / 1000}s`), timeoutMs);
const socket = new WebSocket(url);

socket.addEventListener("error", () => bail("websocket error"));
socket.addEventListener("close", () => bail("websocket closed early"));
socket.addEventListener("message", (event) => {
  let message;
  try {
    message = JSON.parse(String(event.data));
  } catch {
    return;
  }
  if (message.type === "chat-history") {
    if (historyOnly) {
      if (Array.isArray(message.messages) && message.messages.length > 0) {
        console.log(`chat-probe: history has ${message.messages.length} messages`);
        clearTimeout(timer);
        process.exit(0);
      }
      bail("chat-history is empty");
    }
    socket.send(JSON.stringify({ type: "chat", text: "Reply with the single word: pong" }));
    console.log("chat-probe: prompt sent, waiting for first delta");
  } else if (!historyOnly && message.type === "chat-delta") {
    console.log("chat-probe: received chat-delta");
    clearTimeout(timer);
    process.exit(0);
  } else if (!historyOnly && message.type === "chat-error") {
    bail(`chat-error: ${message.error}`);
  }
});
