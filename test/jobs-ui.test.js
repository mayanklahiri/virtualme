import assert from "node:assert/strict";
import test from "node:test";
import { readFile } from "node:fs/promises";

import { filterActivity, jobName, jobSecondary, queueSummary } from "../controller/web/static/js/jobs.js";

test("jobs queue summaries are tailored by envelope type", () => {
  assert.equal(queueSummary({ type: "chat", payload: { text: "hello" } }), "hello");
  assert.equal(queueSummary({ type: "project-run", projectId: "p1", payload: { name: "Daily report" } }), "Daily report");
  assert.equal(queueSummary({ type: "manual-tool", payload: { tool: "bash" } }), "bash");
  assert.equal(queueSummary({ type: "soak-probe", payload: { echo: "soak-1" } }), "soak-1");
});

test("jobs queue rows use short type-derived names with secondary summaries", () => {
  assert.equal(jobName({ type: "chat", payload: { text: "hello" } }), "Chat");
  assert.equal(jobSecondary({ type: "chat", payload: { text: "hello" } }), "hello");
  assert.equal(jobName({ type: "project-run", projectId: "p1", payload: { name: "Daily report" } }), "Project: Daily report");
  assert.equal(jobSecondary({ type: "project-run", selector: "hourly" }), "selector hourly");
  assert.equal(jobSecondary({ type: "project-run" }), "manual run");
  assert.equal(jobName({ type: "manual-tool", payload: { tool: "bash" } }), "Tool: bash");
  assert.equal(jobSecondary({ type: "manual-tool", payload: { tool: "bash", args: { cmd: "ls" } } }), '{"cmd":"ls"}');
  assert.equal(jobName({ type: "soak-probe", payload: { echo: "soak-1" } }), "Queue probe");
});

test("finished queue rows use icons instead of ambiguous status dots", async () => {
  const source = await readFile(new URL("../controller/web/static/js/jobs.js", import.meta.url), "utf8");
  assert.ok(!source.includes("job-result-dot"), "green/red status dot removed");
  assert.ok(source.includes("check"), "success uses a check icon");
  assert.ok(source.includes("circle-x"), "failure uses a circle-x icon");
  assert.ok(source.includes("loader-circle"), "running uses a spinner icon");
});

test("activity filters hide tool calls and jiggler bursts by default", () => {
  const events = [
    { kind: "tool", name: "screenshot" },
    { kind: "tool", name: "jiggle" },
    { kind: "llm", name: "generate" },
    { kind: "speech", name: "speak" },
  ];
  assert.deepEqual(
    filterActivity(events, { showTools: false, showJiggler: false }).map((event) => event.kind),
    ["llm", "speech"],
  );
  assert.deepEqual(
    filterActivity(events, { showTools: true, showJiggler: false }).map((event) => event.name),
    ["screenshot", "generate", "speak"],
  );
  assert.deepEqual(
    filterActivity(events, { showTools: true, showJiggler: true }).map((event) => event.name),
    ["screenshot", "jiggle", "generate", "speak"],
  );
  // Jiggler visibility follows its own toggle even when tools stay hidden.
  assert.deepEqual(
    filterActivity(events, { showTools: false, showJiggler: true }).map((event) => event.name),
    ["jiggle", "generate", "speak"],
  );
});

