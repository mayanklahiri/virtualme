import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const root = new URL("../", import.meta.url);

test("notification route, controls, page, and safe renderer module exist", async () => {
  const [html, router, app, module, assets] = await Promise.all([
    readFile(new URL("controller/web/static/index.html", root), "utf8"),
    readFile(new URL("controller/web/static/js/router.js", root), "utf8"),
    readFile(new URL("controller/web/static/js/app.js", root), "utf8"),
    readFile(new URL("controller/web/static/js/notifications.js", root), "utf8"),
    readFile(new URL("controller/tools/fetch-assets.sh", root), "utf8"),
  ]);
  for (const id of [
    "notification-bell", "notification-popover", "notification-nav-badge",
    "notifications-read-all", "notifications-status", "notification-filters",
    "notification-list", "notification-detail", "notification-detail-curtain",
  ]) {
    assert.match(html, new RegExp(`id="${id}"`), id);
  }
  assert.match(router, /"\/notifications"/);
  assert.match(app, /initNotifications/);
  assert.doesNotMatch(module, /innerHTML/);
  for (const renderer of ["generic", "lifecycle", "configuration", "agent"]) {
    assert.match(module, new RegExp(`${renderer}:`));
  }
  for (const icon of ["bell", "circle-info", "circle-check", "settings"]) {
    assert.match(assets, new RegExp(icon));
  }
});

test("notification module keeps protocol and safety invariants explicit", async () => {
  const source = await readFile(new URL("controller/web/static/js/notifications.js", root), "utf8");
  for (const frame of [
    "notifications-req", "notification-read", "notifications-read-all",
    "notifications-page-req", "notification-detail-req", "notifications-state",
    "notifications-page", "notification-detail", "notification-error",
  ]) {
    assert.match(source, new RegExp(frame));
  }
  assert.match(source, /textContent/);
  assert.match(source, /99\+/);
  assert.match(source, /limit:\s*50/);
  assert.match(source, /slice\(0,\s*10\)/);
  assert.match(source, /Disconnected; notification state may be stale\./);
  assert.match(source, /Notification not found/);
});
