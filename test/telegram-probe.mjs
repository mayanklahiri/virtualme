import assert from "node:assert/strict";
import { appendFileSync, readFileSync } from "node:fs";
import { randomUUID } from "node:crypto";

const base = process.argv[2] ?? "http://127.0.0.1:8080";
const control = process.argv[3];
const record = process.argv[4];
const restart = process.argv.includes("--restart");
const fakeToken = "obviously-fake-runtime-token";
const socket = new WebSocket(base.replace(/^http/, "ws") + "/ws");
/** @type {any[]} */
const frames = [];
/** @type {{predicate: (frame: any) => boolean, after: number, resolve: (frame: any) => void}[]} */
const waiters = [];
socket.addEventListener("message", (event) => {
  const frame = JSON.parse(String(event.data));
  frames.push(frame);
  const index = waiters.findIndex((item) => frames.length - 1 >= item.after && item.predicate(frame));
  if (index >= 0) waiters.splice(index, 1)[0].resolve(frame);
});
const opened = new Promise((resolve, reject) => {
  socket.addEventListener("open", resolve, { once: true });
  socket.addEventListener("error", reject, { once: true });
});

/** @param {(frame: any) => boolean} predicate @param {number} [after] @param {number} [timeout] */
function wait(predicate, after = 0, timeout = 180_000) {
  const buffered = frames.slice(after).find(predicate);
  if (buffered) return Promise.resolve(buffered);
  return new Promise((resolve, reject) => {
    const item = { predicate, after, resolve };
    waiters.push(item);
    setTimeout(() => {
      const index = waiters.indexOf(item);
      if (index >= 0) waiters.splice(index, 1);
      reject(new Error("telegram frame timeout"));
    }, timeout);
  });
}

/** @param {any} action */
function push(action) {
  appendFileSync(control, `${JSON.stringify(action)}\n`, { mode: 0o600 });
}

/** @returns {any[]} */
function requests() {
  return readFileSync(record, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line));
}

try {
  await opened;
  const connected = await wait((frame) => frame.type === "telegram-status" && frame.status.state === "connected");
  assert.equal(connected.status.bot.username, "virtualme_test_bot");
  const destinations = /** @type {any[]} */ (connected.status.destinations);
  assert.deepEqual(destinations.map((item) => item.chatId), ["42", "-100"]);

  const html = await (await fetch(`${base}/telegram`)).text();
  const app = await (await fetch(`${base}/js/app.js`)).text();
  assert.ok(!html.includes(fakeToken) && !app.includes(fakeToken) && !JSON.stringify(connected).includes(fakeToken));

  if (restart) {
    const history = await wait((frame) => frame.type === "chat-history");
    const messages = /** @type {any[]} */ (history.messages);
    for (const updateId of [9, 10]) {
      assert.equal(messages.filter((item) => item.source?.updateId === updateId && item.role === "user").length, 1);
    }
    const polls = requests().filter((item) => item.method === "getUpdates");
    assert.ok(polls.at(-1).body.offset >= 11, `restart offset ${polls.at(-1).body.offset}`);
    const events = await wait((frame) => frame.type === "telegram-events");
    assert.ok(events.eventCount >= 10);
    console.log("telegram-probe restart: OK");
  } else {
    const id = randomUUID();
    socket.send(JSON.stringify({
      type: "telegram-test-send", id, chatId: "42",
      text: "Virtual Me Telegram integration test.",
    }));
    const result = await wait((frame) => frame.type === "telegram-command-result" && frame.id === id);
    assert.equal(result.ok, true, result.error);

    let marker = frames.length;
    push({ error: 409 });
    push({ error: 429, retry_after: 1 });
    const suspended = await wait(
      (frame) => frame.type === "telegram-status" && frame.status.state === "polling_suspended" && frame.status.code === "poll_conflict",
      marker,
    );
    assert.ok(suspended.status.poll.retryAt);
    marker = frames.length;
    const recovered = await wait((frame) => frame.type === "telegram-status" && frame.status.state === "connected", marker);
    assert.equal(recovered.status.poll.consecutiveFailures, 0);
    const retryTrace = requests();
    const limitedIndex = retryTrace.findIndex((item) =>
      item.method === "getUpdatesResult" && item.error === 429 && item.retry_after === 1);
    assert.ok(limitedIndex >= 0, "stub did not serve Retry-After response");
    const nextPoll = retryTrace.slice(limitedIndex + 1).find((item) => item.method === "getUpdates");
    assert.ok(nextPoll, "polling did not resume after Retry-After");
    const retryDelay = nextPoll.ts - retryTrace[limitedIndex].ts;
    assert.ok(retryDelay >= 900, `Retry-After resumed too early (${retryDelay}ms)`);

    const updates = [
      { update_id: 1, message: { message_id: 1, date: 0, text: "denied", chat: { id: 99, type: "private" }, from: { id: 7, is_bot: false } } },
      { update_id: 2, message: { message_id: 2, date: 0, text: "wrong user", chat: { id: 42, type: "private" }, from: { id: 8, is_bot: false } } },
      { update_id: 3, message: { message_id: 3, date: 0, text: "/help", entities: [{ type: "bot_command", offset: 0, length: 5 }], chat: { id: 42, type: "private" }, from: { id: 7, is_bot: false } } },
      { update_id: 4, message: { message_id: 4, date: 0, text: "/status", entities: [{ type: "bot_command", offset: 0, length: 7 }], chat: { id: 42, type: "private" }, from: { id: 7, is_bot: false } } },
      { update_id: 5, message: { message_id: 5, date: 0, text: "/clear", entities: [{ type: "bot_command", offset: 0, length: 6 }], chat: { id: 42, type: "private" }, from: { id: 7, is_bot: false } } },
      { update_id: 6, edited_message: { message_id: 6, date: 0, text: "edited", chat: { id: 42, type: "private" }, from: { id: 7, is_bot: false } } },
      { update_id: 7, message: { message_id: 7, date: 0, text: "bot", chat: { id: 42, type: "private" }, from: { id: 7, is_bot: true } } },
      { update_id: 8, message: { message_id: 8, date: 0, chat: { id: 42, type: "private" }, from: { id: 7, is_bot: false } } },
      { update_id: 9, message: { message_id: 9, date: 0, text: "Reply with exactly ALPHA.", chat: { id: 42, type: "private", first_name: "Alpha" }, from: { id: 7, is_bot: false, username: "allowed" } } },
      { update_id: 10, message: { message_id: 10, date: 0, text: "Reply with exactly BETA.", chat: { id: -100, type: "group", title: "Beta" }, from: { id: 7, is_bot: false, username: "allowed" } } },
    ];
    for (const update of updates.slice(0, 9)) push({ update });
    await wait((frame) => frame.type === "chat-message" && frame.source?.updateId === 9);
    const alpha = await wait((frame) => frame.type === "chat-done" && frame.source?.updateId === 9);
    push({ update: updates[9] });
    await wait((frame) => frame.type === "chat-message" && frame.source?.updateId === 10);
    const beta = await wait((frame) => frame.type === "chat-done" && frame.source?.updateId === 10);
    assert.notEqual(alpha.text, beta.text);

    socket.send(JSON.stringify({ type: "telegram-events-req" }));
    const events = await wait((frame) => frame.type === "telegram-events" && frame.eventCount >= 10);
    const eventItems = /** @type {any[]} */ (events.events);
    const outcomes = new Map(eventItems.map((event) => [event.updateId, event.outcome]));
    assert.equal(outcomes.get(1), "denied");
    assert.equal(outcomes.get(2), "denied");
    assert.equal(outcomes.get(3), "command");
    assert.equal(outcomes.get(4), "command");
    assert.equal(outcomes.get(5), "unknown_command");
    assert.equal(outcomes.get(6), "ignored_edit");
    assert.equal(outcomes.get(7), "ignored_bot");
    assert.equal(outcomes.get(8), "ignored_non_text");
    assert.equal(outcomes.get(9), "accepted");
    assert.equal(outcomes.get(10), "accepted");
    assert.equal(eventItems.find((event) => event.updateId === 1).textPreview, "");

    const sent = requests().filter((item) => item.method === "sendMessage").map((item) => item.body);
    assert.ok(!sent.some((item) => item.chat_id === "99" || item.text === "wrong user"));
    assert.ok(sent.some((item) => item.text.startsWith("Virtual Me shares one conversation")));
    assert.ok(sent.some((item) => item.text.startsWith("Virtual Me is ")));
    assert.ok(sent.some((item) => item.text === "Unknown command. Commands: /help, /status."));
    assert.ok(sent.some((item) => item.chat_id === "42" && item.text === alpha.text));
    assert.ok(sent.some((item) => item.chat_id === "-100" && item.text === beta.text));
    assert.ok(requests().some((item) => item.method === "sendChatAction" && item.body.chat_id === "42"));
    assert.ok(requests().some((item) => item.method === "sendChatAction" && item.body.chat_id === "-100"));
    assert.ok(!readFileSync(record, "utf8").includes(fakeToken));
    console.log("telegram-probe: OK");
  }
} finally {
  socket.close();
}
