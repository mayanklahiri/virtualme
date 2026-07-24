import assert from "node:assert/strict";
import test from "node:test";

import { formatDuration, queueSummary } from "../controller/web/static/js/jobs.js";

test("jobs queue summaries are tailored by envelope type", () => {
  assert.equal(queueSummary({ type: "chat", payload: { text: "hello" } }), "hello");
  assert.equal(queueSummary({ type: "project-run", projectId: "p1", payload: { name: "Daily report" } }), "Daily report");
  assert.equal(queueSummary({ type: "manual-tool", payload: { tool: "bash" } }), "bash");
  assert.equal(queueSummary({ type: "soak-probe", payload: { echo: "soak-1" } }), "soak-1");
});

test("jobs durations stay compact", () => {
  assert.equal(formatDuration(25), "25 ms");
  assert.equal(formatDuration(1250), "1.3 s");
  assert.equal(formatDuration(125000), "2m 5s");
});
