import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { initTelegram, mergeEvents, statusRows, validTestText, validUserID } from "../controller/web/static/js/telegram.js";
import { createFakeDOM } from "./fake-dom.mjs";

const root = new URL("../", import.meta.url);

test("telegram route, safe page, and exact websocket contract", async () => {
  const [html, router, app, page] = await Promise.all([
    readFile(new URL("controller/web/static/index.html", root), "utf8"),
    readFile(new URL("controller/web/static/js/router.js", root), "utf8"),
    readFile(new URL("controller/web/static/js/app.js", root), "utf8"),
    readFile(new URL("controller/web/static/js/telegram.js", root), "utf8"),
  ]);
  assert.match(router, /"\/telegram".*"Telegram"/);
  assert.match(html, /data-page="telegram"/);
  assert.match(html, /telegram-userlist/);
  assert.doesNotMatch(html, /Privacy boundary/);
  assert.match(html, /\/config#integrations-telegram/);
  assert.doesNotMatch(page, /innerHTML|botToken|api\.telegram\.org|config-save/);
  for (const type of [
    "telegram-status-req", "telegram-events-req", "telegram-event-detail-req",
    "telegram-test-send", "telegram-userlist-req", "telegram-userlist-set",
    "telegram-status", "telegram-events", "telegram-userlist",
    "telegram-event", "telegram-event-detail", "telegram-command-result",
  ]) assert.match(app + page, new RegExp(type));
});

test("telegram user ID validation accepts canonical positive IDs", () => {
  assert.equal(validUserID("42"), true);
  assert.equal(validUserID("0"), false);
  assert.equal(validUserID("-1"), false);
});

test("telegram pure UI behavior enforces rune limits, ordering, and status detail", () => {
  assert.equal(validTestText("😀".repeat(4096)), true);
  assert.equal(validTestText("😀".repeat(4097)), false);
  assert.equal(validTestText(" \n\t "), false);
  const original = Array.from({ length: 50 }, (_, index) => ({ id: `e${index}` }));
  const merged = mergeEvents(original, { id: "e20", outcome: "new" });
  assert.equal(merged.length, 50);
  assert.equal(merged[0].id, "e20");
  assert.equal(merged.filter((item) => item.id === "e20").length, 1);
  const rows = new Map(statusRows({
    detail: "Polling", bot: { username: "vm" },
    poll: { nextOffset: 9, consecutiveFailures: 3, retryAt: 1, lastSuccessTs: 2 },
  }));
  assert.equal(rows.get("Username"), "@vm");
  assert.equal(rows.get("Failures"), "3");
  assert.notEqual(rows.get("Retry at"), "Not scheduled");
});

test("telegram DOM flow disables controls and correlates results and details", () => {
  const ids = [
    "telegram-state", "telegram-details", "telegram-test-form", "telegram-destination",
    "telegram-test-text", "telegram-test-send", "telegram-test-result", "telegram-events-card",
    "telegram-event-list", "telegram-event-detail", "telegram-event-meta",
    "telegram-event-error", "telegram-event-raw", "telegram-event-close",
    "telegram-userlist-rows", "telegram-user-input", "telegram-userlist-save", "telegram-userlist-status",
    "telegram-user-add",
  ];
  const { nodes, body, document } = createFakeDOM(ids.map((id) => `#${id}`));
  for (const selector of [
    "#telegram-test-result", "#telegram-event-detail", "#telegram-event-error",
  ]) nodes.get(selector).hidden = true;
  const priorDocument = globalThis.document;
  globalThis.document = /** @type {any} */ (document);
  try {
    /** @type {any[]} */
    const sent = [];
    const ui = initTelegram((message) => sent.push(message));
    const button = nodes.get("#telegram-test-send");
    const destination = nodes.get("#telegram-destination");
    const text = nodes.get("#telegram-test-text");
    ui.connection("live");
    ui.frame({ type: "telegram-status", status: {
      enabled: true, state: "connected", detail: "Polling",
      destinations: [{ chatId: "42", label: "Mayank" }], poll: {},
    } });
    assert.equal(destination.disabled, false);
    assert.equal(destination.options[0].value, "42");
    destination.value = "42";
    assert.equal(button.disabled, true);
    text.value = " hello ";
    text.dispatch("input");
    assert.equal(button.disabled, false);
    nodes.get("#telegram-test-form").dispatch("submit");
    assert.equal(button.disabled, true);
    const sendRequest = sent.at(-1);
    assert.equal(sendRequest.type, "telegram-test-send");
    assert.equal(sendRequest.chatId, "42");
    assert.equal(sendRequest.text, "hello");
    ui.frame({ type: "telegram-command-result", id: "wrong", ok: true });
    assert.equal(nodes.get("#telegram-test-result").hidden, true);
    ui.frame({ type: "telegram-command-result", id: sendRequest.id, ok: true });
    assert.equal(nodes.get("#telegram-test-result").textContent, "Test message sent.");
    assert.equal(nodes.get("#telegram-test-result").focused, true);

    ui.frame({ type: "telegram-events", events: [{ id: "event-1", outcome: "accepted" }] });
    nodes.get("#telegram-event-list").children[0].dispatch("click");
    const detailRequest = sent.at(-1);
    assert.equal(detailRequest.type, "telegram-event-detail-req");
    assert.equal(detailRequest.id, "event-1");
    ui.frame({ type: "telegram-event-detail", requestId: "stale", event: { outcome: "wrong" } });
    assert.equal(nodes.get("#telegram-event-detail").hidden, true);
    ui.frame({ type: "telegram-event-detail", requestId: detailRequest.requestId, event: {
      outcome: "accepted", updateId: 1, rawOmitted: true,
    } });
    assert.equal(nodes.get("#telegram-event-detail").hidden, false);
    assert.equal(nodes.get("#telegram-event-detail").classList.contains("open"), true);
    assert.equal(body.classList.contains("telegram-detail-locked"), true);
    nodes.get("#telegram-event-close").dispatch("click");
    assert.equal(nodes.get("#telegram-event-detail").hidden, true);
    assert.equal(body.classList.contains("telegram-detail-locked"), false);
  } finally {
    globalThis.document = priorDocument;
  }
});
