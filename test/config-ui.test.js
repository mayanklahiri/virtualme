import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

import {
  advancedGroups,
  configAnchor,
  conflictMessage,
  issueControl,
  orderedSections,
  orderedSettings,
  parseEditorValue,
  restartComplete,
  restartMessage,
  setConfigPath,
  validateSecretReference,
} from "../controller/web/static/js/config-model.js";

test("config model orders, anchors, converts, and protects secrets", () => {
  assert.equal(configAnchor("llama.contextTokens"), "llama-context-tokens");
  assert.deepEqual(
    orderedSections([
      { id: "late", ui: { order: 20 } },
      { id: "early", ui: { order: 10 } },
    ]).map(({ id }) => id),
    ["early", "late"],
  );
  assert.equal(parseEditorValue("42", { type: "integer" }), 42);
  assert.equal(parseEditorValue(true, { type: "boolean" }), true);
  assert.equal(validateSecretReference("${file:/run/secrets/smtp}"), true);
  assert.equal(validateSecretReference("DO_NOT_LEAK_031"), false);
  assert.match(restartMessage(["llama", "controller"]), /llama.*controller/);
  assert.deepEqual(parseEditorValue(["first", "second"], {
    type: "array", constraints: { uniqueItems: true },
  }), ["first", "second"]);
  assert.throws(() => parseEditorValue(["same", "same"], {
    type: "array", constraints: { uniqueItems: true },
  }), /unique/);
});

test("config model handles grouping, issues, conflicts, discard data, and reconnect", () => {
  const settings = [
    { path: "z", ui: { order: 20, advanced: true } },
    { path: "a", ui: { order: 10, advanced: false } },
  ];
  assert.deepEqual(orderedSettings(settings).map(({ path }) => path), ["a", "z"]);
  assert.deepEqual(advancedGroups(settings), {
    regular: [settings[1]],
    advanced: [settings[0]],
  });
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
  assert.match(module, /Environment reference/);
  assert.match(module, /config-list-row/);
  assert.match(module, /60_000/);
  assert.match(module, /restartComplete/);
  assert.doesNotMatch(module, /lastRefreshAt.*value/);
});
