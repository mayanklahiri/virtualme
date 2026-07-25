import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";

const base = process.argv[2] ?? "http://127.0.0.1:8080";
const socket = new WebSocket(base.replace(/^http/, "ws") + "/ws");
/** @type {any[]} */
const frames = [];
/** @type {{predicate: (frame: any) => boolean, resolve: (frame: any) => void}[]} */
const waiters = [];
socket.addEventListener("message", (event) => {
  const frame = JSON.parse(String(event.data));
  frames.push(frame);
  const index = waiters.findIndex((item) => item.predicate(frame));
  if (index >= 0) waiters.splice(index, 1)[0].resolve(frame);
});
const opened = new Promise((resolve, reject) => {
  socket.addEventListener("open", resolve, { once: true });
  socket.addEventListener("error", reject, { once: true });
});
/** @param {(frame: any) => boolean} predicate */
function wait(predicate) {
  const buffered = frames.find(predicate);
  if (buffered) return Promise.resolve(buffered);
  return new Promise((resolve, reject) => {
    const item = { predicate, resolve };
    waiters.push(item);
    setTimeout(() => reject(new Error("telegram frame timeout")), 30_000);
  });
}

try {
  await opened;
  const statusFrame = await wait((/** @type {any} */ frame) => frame.type === "telegram-status" && frame.status.state === "connected");
  assert.equal(statusFrame.status.bot.username, "virtualme_test_bot");
  assert.ok(statusFrame.status.destinations.length > 0);
  const id = randomUUID();
  socket.send(JSON.stringify({
    type: "telegram-test-send", id,
    chatId: statusFrame.status.destinations[0].chatId,
    text: "Virtual Me Telegram integration test.",
  }));
  const result = await wait((/** @type {any} */ frame) => frame.type === "telegram-command-result" && frame.id === id);
  assert.equal(result.ok, true, result.error);
  socket.send(JSON.stringify({ type: "telegram-events-req" }));
  const events = await wait((/** @type {any} */ frame) => frame.type === "telegram-events");
  assert.ok(Array.isArray(events.events));
  console.log("telegram-probe: OK");
} finally {
  socket.close();
}
