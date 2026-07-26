import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

import {
  buildSettingTree,
  configAnchor,
  conflictMessage,
  issueControl,
  orderedSections,
  orderedSettings,
  parseEditorValue,
  restartComplete,
  restartMessage,
  secretStatusLabel,
  setConfigPath,
  validateStringItem,
  validateSecretReference,
} from "../controller/web/static/js/config-model.js";
import { initConfig } from "../controller/web/static/js/config.js";
import { createFakeDOM } from "./fake-dom.mjs";

test("config model orders, anchors, converts, and protects secrets", () => {
  assert.equal(configAnchor("llama.contextTokens"), "llama-context-tokens");
  assert.deepEqual(
    orderedSections([
      { id: "late", ui: { order: 20 } },
      { id: "early", ui: { order: 10 } },
    ]).map(({ id }) => id),
    ["early", "late"],
  );
  const tree = buildSettingTree([
    { path: "mail.smarthost.host", ui: { order: 2 } },
    { path: "mail.from", ui: { order: 1 } },
  ]);
  assert.equal(tree.children.get("mail").settings.length, 1);
  assert.equal(tree.children.get("mail").children.get("smarthost").settings[0].path, "mail.smarthost.host");
  assert.equal(parseEditorValue("42", { type: "integer" }), 42);
  assert.equal(parseEditorValue(true, { type: "boolean" }), true);
  assert.equal(validateSecretReference("${file:/run/secrets/smtp}"), true);
  assert.equal(validateSecretReference("${file:${data}/secrets/telegram-token}"), true);
  assert.equal(validateSecretReference("${file:relative/no-token}"), false);
  assert.equal(validateSecretReference("DO_NOT_LEAK_031"), false);
  assert.match(restartMessage(["llama", "controller"]), /llama.*controller/);
  assert.deepEqual(parseEditorValue(["first", "second"], {
    type: "array", constraints: { uniqueItems: true },
  }), ["first", "second"]);
  assert.throws(() => parseEditorValue(["same", "same"], {
    type: "array", constraints: { uniqueItems: true },
  }), /unique/);
  assert.equal(validateStringItem("-100", {
    type: "string", constraints: { pattern: "^-?[1-9][0-9]*$" },
  }), "-100");
  assert.throws(() => parseEditorValue(["not-an-id"], {
    type: "array",
    item: { type: "string", constraints: { pattern: "^-?[1-9][0-9]*$" } },
  }), /pattern/);
  assert.equal(secretStatusLabel({ configured: true, resolved: true }), "Resolved");
  assert.equal(secretStatusLabel({ configured: true, resolved: false, status: "inactive" }), "Inactive");
});

test("config model handles grouping, issues, conflicts, discard data, and reconnect", () => {
  const settings = [
    { path: "z", ui: { order: 20, advanced: true } },
    { path: "a", ui: { order: 10, advanced: false } },
  ];
  assert.deepEqual(orderedSettings(settings).map(({ path }) => path), ["a", "z"]);
  assert.equal(buildSettingTree(settings).settings.length, 2);
  const focused = { focus() {} };
  assert.deepEqual(issueControl([{ path: "a" }], new Map([["a", focused]])), {
    issue: { path: "a" }, control: focused,
  });
  assert.match(conflictMessage({ error: { code: "config_conflict" } }), /Reload/);
  const raw = { agent: { maxSteps: 10 } };
  setConfigPath(raw, "agent.maxSteps", 20);
  assert.equal(raw.agent.maxSteps, 20);
  assert.equal(restartComplete({
    fileHash: "hash", startupHash: "hash", pendingRestart: false,
  }, "hash"), true);
  assert.equal(restartComplete({
    fileHash: "hash", startupHash: "old", pendingRestart: true,
  }, "hash"), false);
});

test("config route is static, safe, and schema-driven", async () => {
  const root = new URL("../controller/web/static/", import.meta.url);
  const [html, router, module] = await Promise.all([
    readFile(new URL("index.html", root), "utf8"),
    readFile(new URL("js/router.js", root), "utf8"),
    readFile(new URL("js/config.js", root), "utf8"),
  ]);
  assert.match(html, /href="\/config"/);
  assert.match(html, /data-page="config"/);
  assert.match(router, /\["\/config", \["config", "Config"\]\]/);
  assert.doesNotMatch(module, /innerHTML/);
  assert.match(module, /componentRenderers/);
  assert.match(module, /buildSettingTree/);
  assert.match(module, /config-list-row/);
  assert.doesNotMatch(module, /Environment reference/);
  assert.doesNotMatch(module, /Advanced/);
  assert.match(module, /input\.required = true/);
  assert.match(module, /60_000/);
  assert.match(module, /restartComplete/);
  assert.match(module, /\/api\/config\/secrets\/refresh/);
  assert.match(module, /beginRestart\(response\.pendingHash\)/);
  assert.doesNotMatch(module, /Secret status/);
  assert.doesNotMatch(module, /secret\.(value|bytes|length|hash)/);
});

test("config DOM flow loads, edits, reports conflict, and discards", async () => {
  const selectors = [
    "#config-content", "#config-edit", "#config-save", "#config-discard",
    "#config-restart", "#config-status",
  ];
  const { nodes, document } = createFakeDOM(selectors);
  const priorDocument = globalThis.document;
  const priorFetch = globalThis.fetch;
  const snapshot = {
    raw: { version: 1 }, effective: { version: 1 }, sources: {}, secrets: {},
    fileHash: "file-a", startupHash: "file-a", pendingRestart: false, restartServices: [],
  };
  /** @type {any[]} */
  const requests = [];
  globalThis.document = /** @type {any} */ (document);
  /** @param {string} url @param {any} [options] */
  const fakeFetch = async (url, options = {}) => {
    requests.push({ url, options });
    if (url === "/api/config/schema") return { ok: true, json: async () => ({ sections: [] }) };
    if (url === "/api/config" && options.method === "PUT") {
      return {
        ok: false,
        statusText: "Conflict",
        json: async () => ({ error: { code: "config_conflict", message: "stale" } }),
      };
    }
    if (url === "/api/config") return { ok: true, json: async () => snapshot };
    throw new Error(`unexpected request ${url}`);
  };
  globalThis.fetch = /** @type {any} */ (fakeFetch);
  try {
    const ui = initConfig();
    ui.show("config");
    await new Promise((resolve) => setTimeout(resolve, 0));
    assert.equal(nodes.get("#config-edit").hidden, false);
    assert.equal(nodes.get("#config-save").hidden, true);
    nodes.get("#config-edit").dispatch("click");
    assert.equal(nodes.get("#config-edit").hidden, true);
    assert.equal(nodes.get("#config-save").hidden, false);
    await nodes.get("#config-save").dispatch("click");
    assert.match(nodes.get("#config-status").textContent, /changed in another session/i);
    assert.equal(requests.filter(({ options }) => options.method === "PUT").length, 1);
    nodes.get("#config-discard").dispatch("click");
    assert.equal(nodes.get("#config-edit").hidden, false);
    assert.equal(nodes.get("#config-save").hidden, true);
  } finally {
    globalThis.document = priorDocument;
    globalThis.fetch = priorFetch;
  }
});

test("supervised services consume split endpoints and X11 socket path", async () => {
  const root = new URL("../docker/rootfs/etc/s6-overlay/s6-rc.d/", import.meta.url);
  const [xvfb, x11vnc, novnc, chromium, valkey, llama, watchdog] = await Promise.all([
    readFile(new URL("svc-xvfb/run", root), "utf8"),
    readFile(new URL("svc-x11vnc/run", root), "utf8"),
    readFile(new URL("svc-novnc/run", root), "utf8"),
    readFile(new URL("svc-chromium/run", root), "utf8"),
    readFile(new URL("svc-valkey/run", root), "utf8"),
    readFile(new URL("svc-llama/run", root), "utf8"),
    readFile(new URL("svc-chromium-watchdog/run", root), "utf8"),
  ]);
  assert.match(xvfb, /VM_EFFECTIVE_X11_SOCKET_DIR/);
  assert.match(x11vnc, /VM_EFFECTIVE_VNC_HOST/);
  assert.match(x11vnc, /VM_EFFECTIVE_VNC_PORT/);
  assert.match(x11vnc, /-listen6/);
  assert.match(x11vnc, /-no6/);
  assert.match(x11vnc, /-cursor most -nocursorshape/);
  assert.match(novnc, /VM_EFFECTIVE_NOVNC_HOST/);
  assert.match(chromium, /VM_EFFECTIVE_CDP_HOST/);
  assert.match(valkey, /VM_EFFECTIVE_VALKEY_HOST/);
  assert.match(llama, /VM_EFFECTIVE_LLAMA_HOST/);
  assert.match(watchdog, /getwindowfocus/);
  assert.match(watchdog, /xdotool windowfocus "\$WIN"/);
  for (const script of [x11vnc, novnc, chromium, valkey, llama]) {
    assert.doesNotMatch(script, /VM_EFFECTIVE_[A-Z_]+(?:%|##)/);
  }
});
