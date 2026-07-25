import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

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
  assert.match(html, /\/config#integrations-telegram/);
  assert.doesNotMatch(page, /innerHTML|botToken|api\.telegram\.org|config-save/);
  for (const type of [
    "telegram-status-req", "telegram-events-req", "telegram-event-detail-req",
    "telegram-test-send", "telegram-status", "telegram-events",
    "telegram-event", "telegram-event-detail", "telegram-command-result",
  ]) assert.match(app + page, new RegExp(type));
});
