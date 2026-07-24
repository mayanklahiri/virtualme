import assert from "node:assert/strict";
import test from "node:test";

import { formatDuration, jobName, jobSecondary, queueSummary } from "../controller/web/static/js/jobs.js";

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

test("jobs durations stay compact", () => {
  assert.equal(formatDuration(25), "25 ms");
  assert.equal(formatDuration(1250), "1.3 s");
  assert.equal(formatDuration(125000), "2m 5s");
});
