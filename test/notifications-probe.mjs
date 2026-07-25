// Two-client durable notification protocol probe (Node >= 22).
import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";

const mode = process.argv[2]?.startsWith("--") ? process.argv[2] : "";
const base = (mode ? process.argv[3] : process.argv[2]) ?? "http://127.0.0.1:8080";
const url = base.replace(/^http/, "ws") + "/ws";
const timeoutMs = 60_000;

function client() {
  const socket = new WebSocket(url);
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
  return {
    socket,
    opened,
    /** @param {any} value */
    send(value) { socket.send(JSON.stringify(value)); },
    /** @param {(frame: any) => boolean} predicate */
    wait(predicate) {
      const buffered = frames.find(predicate);
      if (buffered) return Promise.resolve(buffered);
      return new Promise((resolve, reject) => {
        const item = { predicate, resolve };
        waiters.push(item);
        setTimeout(() => {
          const index = waiters.indexOf(item);
          if (index >= 0) waiters.splice(index, 1);
          reject(new Error("notification frame timeout"));
        }, timeoutMs);
      });
    },
  };
}

if (mode === "--verify-lifecycle") {
  const expectedID = process.argv[4];
  const expectedUnclean = Number(process.argv[5]);
  const probe = client();
  try {
    await probe.opened;
    const requestId = randomUUID();
    probe.send({ type: "notifications-page-req", requestId, before: "", limit: 50 });
    const page = await probe.wait((frame) => frame.type === "notifications-page" && frame.requestId === requestId);
    const subtypes = page.notifications.map((/** @type {any} */ item) => item.subtype);
    for (const subtype of ["config-restart-shutdown", "config-restart-startup", "clean-shutdown"]) {
      assert.ok(subtypes.includes(subtype), `missing lifecycle subtype ${subtype}`);
    }
    assert.equal(subtypes.filter((/** @type {any} */ value) => value === "unclean-startup").length, expectedUnclean);
    const created = page.notifications.find((/** @type {any} */ item) => item.id === expectedID);
    assert.ok(created?.readAtMs > 0, "created notification or global read state did not persist");
    console.log(`notifications-probe: lifecycle ok unclean=${expectedUnclean}`);
  } finally {
    probe.socket.close();
  }
} else {
  const a = client();
  const b = client();
  try {
  await Promise.all([a.opened, b.opened]);
  a.send({ type: "tools-list-req" });
  const tools = await a.wait((frame) => frame.type === "tools-list");
  const notify = tools.tools.find((/** @type {any} */ tool) => tool.name === "notify");
  assert.ok(notify, "notify tool missing");
  assert.equal(notify.schema.additionalProperties, false);

  const invocation = randomUUID();
  a.send({
    type: "tool-invoke", id: invocation, tool: "notify",
    args: { type: "success", subtype: "e2e", title: `Notification ${invocation}`,
      summary: "Durable cross-client protocol probe.", detail: { invocation } },
  });
  const result = await a.wait((frame) => frame.type === "tool-result" && frame.id === invocation);
  assert.equal(result.ok, true, result.error);
  assert.match(result.text, /^[0-9A-HJKMNP-TV-Z]{26}$/);
  const id = result.text;
  const [stateA, stateB] = await Promise.all([
    a.wait((frame) => frame.type === "notifications-state" && frame.notifications.some((/** @type {any} */ item) => item.id === id)),
    b.wait((frame) => frame.type === "notifications-state" && frame.notifications.some((/** @type {any} */ item) => item.id === id)),
  ]);
  assert.equal(stateA.unread, stateB.unread);
  a.send({ type: "notification-read", requestId: randomUUID(), id });
  const readA = await a.wait((frame) => frame.type === "notifications-state" && frame.change?.kind === "read");
  const readB = await b.wait((frame) => frame.type === "notifications-state" && frame.change?.kind === "read");
  assert.equal(readA.change.readAtMs, readB.change.readAtMs);
  b.send({ type: "notifications-read-all", requestId: randomUUID() });
  await Promise.all([
    a.wait((frame) => frame.type === "notifications-state" && frame.unread === 0),
    b.wait((frame) => frame.type === "notifications-state" && frame.unread === 0),
  ]);
  console.log(`notifications-probe: ok ${id}`);
  } finally {
    a.socket.close();
    b.socket.close();
  }
}
