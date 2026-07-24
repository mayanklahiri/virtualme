// Live soak flows against a running controller endpoint (spec 012 §5a).
// The initial flows exercise the browser agent via chat; the runner is
// feature-agnostic and future flows may cover speech, mail, metrics, etc.
// Real LLM + real browser, so outcomes are layered:
//   - hard assertions (deterministic agent behavior: tools ran, observation
//     text contained known facts) fail the flow;
//   - soft assertions (the model's prose) only WARN.
// Zero dependencies; Node >= 22 (global WebSocket).
//
// Env: SOAK_URL     ws endpoint      (default ws://127.0.0.1:8080/ws)
//      SOAK_TIMEOUT per-flow seconds (default 600)
//      SOAK_FLOW    regex selecting flows by name (default: all)

import { randomUUID } from "node:crypto";

const URL_WS = process.env.SOAK_URL ?? "ws://127.0.0.1:8080/ws";
const FLOW_TIMEOUT_MS = Number(process.env.SOAK_TIMEOUT ?? 600) * 1000;
const FLOW_FILTER = new RegExp(process.env.SOAK_FLOW ?? ".");

const tty = process.stdout.isTTY;
/** @param {number} code @param {string} text */
const paint = (code, text) => (tty ? `\x1b[${code}m${text}\x1b[0m` : text);
const dim = (/** @type {string} */ t) => paint(2, t);
const cyan = (/** @type {string} */ t) => paint(36, t);
const green = (/** @type {string} */ t) => paint(32, t);
const red = (/** @type {string} */ t) => paint(31, t);
const yellow = (/** @type {string} */ t) => paint(33, t);
const bold = (/** @type {string} */ t) => paint(1, t);

const startedAt = Date.now();
/** @param {string} scope @param {string} message */
function log(scope, message) {
  const elapsed = ((Date.now() - startedAt) / 1000).toFixed(1).padStart(7);
  console.log(`${dim(elapsed + "s")} ${dim("[")}${cyan(scope)}${dim("]")} ${message}`);
}

/** @param {string} text @param {number} n */
const excerpt = (text, n = 160) =>
  text.length <= n ? text : text.slice(0, n) + `… (${text.length} bytes)`;

/**
 * @typedef {{ tool: string, summary: string, text: string, error?: string,
 *             args: unknown, screenshot?: string, n: number }} Step
 * @typedef {{ steps: Step[], reply: string, chatError: string, probeProblems?: string[] }} FlowResult
 * @typedef {{ name: string, prompt?: string,
 *             run?: (ws: WebSocket, log: (message: string) => void) => Promise<FlowResult>,
 *             hard: (r: FlowResult) => string[],
 *             soft: (r: FlowResult) => string[] }} Flow
 */

/**
 * Count non-overlapping occurrences of needle in haystack.
 * @param {string} haystack @param {string} needle
 */
function count(haystack, needle) {
  let total = 0;
  for (let at = haystack.indexOf(needle); at >= 0; at = haystack.indexOf(needle, at + needle.length)) total++;
  return total;
}

/** @param {FlowResult} r @param {string} tool */
const stepsFor = (r, tool) => r.steps.filter((s) => s.tool === tool);
/** @param {FlowResult} r @param {string} tool */
const textOf = (r, tool) => stepsFor(r, tool).map((s) => s.text).join("\n");

/**
 * Decoded byte size of a base64 data URL.
 * @param {string} url
 */
function dataURLBytes(url) {
  const comma = url.indexOf(",");
  if (comma < 0) return 0;
  return Math.floor(((url.length - comma - 1) * 3) / 4);
}

/** @type {Flow[]} */
const flows = [
  {
    name: "lahiri-dom",
    prompt:
      "Navigate to https://www.lahiri.me and wait for the page to load. " +
      "Then read the page's DOM, tell me whose homepage it is and where they currently work, " +
      "and take a screenshot of the page. You must call the screenshot tool before your final " +
      "answer; merely saying that you took a screenshot is not sufficient.",
    hard(r) {
      const problems = [];
      const nav = textOf(r, "navigate");
      if (stepsFor(r, "navigate").length === 0) problems.push("no navigate step ran");
      else if (!/lahiri\.me/i.test(nav)) problems.push(`navigate observation lacks lahiri.me URL: ${excerpt(nav)}`);
      else if (!nav.includes('"ready":true')) problems.push(`navigate observation not settled (ready:true missing): ${excerpt(nav)}`);
      const dom = textOf(r, "dom");
      if (stepsFor(r, "dom").length === 0) problems.push("no dom step ran");
      else {
        if (!/lahiri/i.test(dom)) problems.push(`dom observation lacks "Lahiri": ${excerpt(dom)}`);
        if (!/oracle/i.test(dom)) problems.push(`dom observation lacks "Oracle": ${excerpt(dom)}`);
      }
      const shots = stepsFor(r, "screenshot");
      if (shots.length === 0) problems.push("no screenshot step ran");
      else {
        if (!/screenshot 1024x\d+ API space/.test(shots[0].text))
          problems.push(`screenshot not resized to 1024-wide API space: ${excerpt(shots[0].text)}`);
        const thumb = shots.find((s) => s.screenshot);
        if (thumb?.screenshot && dataURLBytes(thumb.screenshot) > 32 * 1024)
          problems.push(`screenshot thumbnail exceeds 32 KiB (${dataURLBytes(thumb.screenshot)} bytes)`);
      }
      return problems;
    },
    soft(r) {
      const problems = [];
      if (!/lahiri/i.test(r.reply)) problems.push('final reply does not mention "Lahiri"');
      if (!/oracle/i.test(r.reply)) problems.push('final reply does not mention "Oracle"');
      return problems;
    },
  },
  {
    name: "hn-top10",
    prompt:
      "Navigate to https://news.ycombinator.com and wait for it to load. " +
      "Then read the DOM and list the titles of the top 10 articles.",
    hard(r) {
      const problems = [];
      if (stepsFor(r, "navigate").length === 0) problems.push("no navigate step ran");
      const dom = textOf(r, "dom");
      if (stepsFor(r, "dom").length === 0) problems.push("no dom step ran");
      else {
        if (!dom.includes("Hacker News")) problems.push(`dom observation lacks "Hacker News": ${excerpt(dom)}`);
        const stories = count(dom, "item?id=");
        if (stories < 10) problems.push(`dom observation shows ${stories} story rows (item?id=), want >= 10`);
      }
      return problems;
    },
    soft(r) {
      const listed = (r.reply.match(/^\s*(?:\d+[.)]|[-*])\s+\S/gm) ?? []).length;
      return listed >= 5 ? [] : [`final reply lists ${listed} items, want >= 5`];
    },
  },
  {
    name: "readpage-example",
    prompt:
      "Navigate to https://example.com, wait for it to load, then use read_page " +
      "and tell me the page's main heading.",
    hard(r) {
      const problems = [];
      if (stepsFor(r, "navigate").length === 0) problems.push("no navigate step ran");
      const seen = textOf(r, "read_page") + textOf(r, "dom");
      if (!seen.includes("Example Domain"))
        problems.push(`no observation contains "Example Domain": ${excerpt(seen)}`);
      return problems;
    },
    soft(r) {
      return /example domain/i.test(r.reply) ? [] : ['final reply does not mention "Example Domain"'];
    },
  },
  {
    name: "tools-roundtrip",
    run(ws, flowLog) {
      return new Promise((resolve, reject) => {
        const required = [
          "screenshot", "dom", "read_page", "click", "click_element", "type",
          "type_into", "key", "scroll", "navigate", "bash", "system_info", "speak",
          "dom_query", "dom_validate", "page_eval", "layout_debug",
        ];
        const id = randomUUID();
        /** @type {string[]} */
        const problems = [];
        let invoked = false;
        const timer = setTimeout(() => reject(new Error("tools roundtrip timed out after 60s")), 60000);
        onFrame = (frame) => {
          if (frame.type === "tools-list" && !invoked) {
            invoked = true;
            const names = Array.isArray(frame.tools)
              ? frame.tools.map((/** @type {any} */ tool) => tool.name)
              : [];
            flowLog(`received ${names.length} tool definitions`);
            if (names.length < 15) problems.push(`tool list has ${names.length} entries, want >= 15`);
            for (const name of required) {
              if (!names.includes(name)) problems.push(`tool list is missing ${name}`);
            }
            ws.send(JSON.stringify({
              type: "tool-invoke", id, tool: "system_info", args: { topic: "os" },
            }));
            return;
          }
          if (frame.type === "tool-result" && frame.id === id) {
            clearTimeout(timer);
            if (frame.ok !== true) problems.push(`manual system_info failed: ${frame.error || "unknown error"}`);
            if (!String(frame.text ?? "").trim()) problems.push("manual system_info returned empty text");
            resolve({ steps: [], reply: "", chatError: "", probeProblems: problems });
          }
        };
        ws.send(JSON.stringify({ type: "tools-list-req" }));
      });
    },
    hard(r) {
      return r.probeProblems ?? [];
    },
    soft() {
      return [];
    },
  },
  {
    name: "jobs-queue-probe",
    run(ws, flowLog) {
      return new Promise((resolve, reject) => {
        /** @type {string[]} */
        const ids = [];
        /** @type {any[]} */
        const states = [];
        let requestedActivity = false;
        const timer = setTimeout(() => reject(new Error("jobs queue probe timed out after 60s")), 60000);
        onFrame = (frame) => {
          if (frame.type === "job-pushed") {
            ids.push(String(frame.id ?? ""));
            flowLog(`pushed ${ids.length}: ${frame.id}`);
            if (ids.length === 1) {
              ws.send(JSON.stringify({ type: "job-push", job: { type: "soak-probe", payload: { echo: "soak-2" } } }));
            }
            return;
          }
          if (frame.type === "queue-state") {
            states.push(frame);
            const bothFinished = ids.length === 2 && ids.every((id) =>
              (/** @type {any[]} */ (frame.finished ?? [])).some((job) => job.id === id && job.result?.ok === true),
            );
            if (bothFinished && !requestedActivity) {
              requestedActivity = true;
              ws.send(JSON.stringify({ type: "activity-req" }));
            }
            return;
          }
          if (frame.type === "activity" && requestedActivity) {
            clearTimeout(timer);
            const problems = [];
            if (!Array.isArray(frame.events)) problems.push("activity reply events is not an array");
            const ordered = states.some((state) =>
              state.running?.id === ids[0] &&
              (/** @type {any[]} */ (state.upcoming ?? [])).some((job) => job.id === ids[1]),
            );
            if (!ordered) problems.push("never saw soak-1 running while soak-2 was upcoming");
            const final = states.findLast((state) =>
              ids.every((id) => (/** @type {any[]} */ (state.finished ?? [])).some((job) => job.id === id)),
            );
            const first = (/** @type {any[]} */ (final?.finished ?? [])).find((job) => job.id === ids[0]);
            const second = (/** @type {any[]} */ (final?.finished ?? [])).find((job) => job.id === ids[1]);
            if (!first?.result?.ok || !second?.result?.ok) problems.push("both probes did not finish successfully");
            if (Number(first?.result?.finishedTs) >= Number(second?.result?.finishedTs)) {
              problems.push("soak-1 did not finish before soak-2");
            }
            resolve({ steps: [], reply: "", chatError: "", probeProblems: problems });
          }
        };
        ws.send(JSON.stringify({ type: "job-push", job: { type: "soak-probe", payload: { echo: "soak-1" } } }));
      });
    },
    hard(r) {
      return r.probeProblems ?? [];
    },
    soft() {
      return [];
    },
  },
];

/** One shared socket; frames are dispatched to the active flow. */
const socket = new WebSocket(URL_WS);
/** @type {((frame: any) => void) | null} */
let onFrame = null;

socket.addEventListener("message", (event) => {
  let frame;
  try {
    frame = JSON.parse(String(event.data));
  } catch {
    return;
  }
  onFrame?.(frame);
});
socket.addEventListener("error", () => {
  console.error(red(`soak: websocket error connecting to ${URL_WS}`));
  process.exit(2);
});

await new Promise((resolve) => socket.addEventListener("open", resolve, { once: true }));
log("soak", `connected to ${URL_WS}`);

/** Clear the shared conversation and wait for the empty-history broadcast. */
function clearChat() {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error("chat-clear timed out")), 15000);
    onFrame = (frame) => {
      if (frame.type === "chat-history" && Array.isArray(frame.messages) && frame.messages.length === 0) {
        clearTimeout(timer);
        resolve(undefined);
      }
      if (frame.type === "chat-error") log("soak", yellow(`chat-clear: ${frame.error}`));
    };
    socket.send(JSON.stringify({ type: "chat-clear" }));
  });
}

/**
 * Run one flow: send the chat prompt, stream fine-grained event logs, and
 * collect steps + final reply until chat-done.
 * @param {Flow} flow
 * @returns {Promise<FlowResult>}
 */
function runFlow(flow) {
  return new Promise((resolve, reject) => {
    /** @type {FlowResult} */
    const result = { steps: [], reply: "", chatError: "" };
    let deltaBytes = 0;
    let lastPhase = "";
    const timer = setTimeout(
      () => reject(new Error(`flow timed out after ${FLOW_TIMEOUT_MS / 1000}s`)),
      FLOW_TIMEOUT_MS,
    );
    onFrame = (frame) => {
      switch (frame.type) {
        case "agent-status":
          if (frame.phase !== lastPhase) {
            lastPhase = frame.phase;
            log(flow.name, dim(`agent ${frame.phase} (step ${frame.n})`));
          }
          break;
        case "agent-step": {
          /** @type {Step} */
          const step = {
            tool: String(frame.tool ?? ""),
            summary: String(frame.summary ?? ""),
            text: String(frame.text ?? ""),
            args: frame.args,
            n: Number(frame.n ?? 0),
            screenshot: typeof frame.screenshot === "string" ? frame.screenshot : undefined,
          };
          if (frame.error) step.error = String(frame.error);
          result.steps.push(step);
          const detail = step.error
            ? red(`error: ${step.error}`)
            : dim(step.text ? excerpt(step.text.replaceAll("\n", " ")) : "(no observation text)");
          log(flow.name, `step ${step.n} ${bold(step.tool)} ${JSON.stringify(step.args ?? {})} — ${detail}`);
          break;
        }
        case "chat-delta":
          result.reply += String(frame.text ?? "");
          deltaBytes += String(frame.text ?? "").length;
          break;
        case "chat-error":
          result.chatError = String(frame.error ?? "unknown");
          log(flow.name, red(`chat-error: ${result.chatError}`));
          break;
        case "chat-done":
          clearTimeout(timer);
          if (typeof frame.text === "string" && frame.text.trim()) result.reply = frame.text;
          log(flow.name, `reply (${deltaBytes} streamed bytes): ${dim(excerpt(result.reply.replaceAll("\n", " "), 300))}`);
          resolve(result);
          break;
      }
    };
    log(flow.name, `prompt: ${dim(flow.prompt ?? "")}`);
    socket.send(JSON.stringify({ type: "chat", text: flow.prompt ?? "" }));
  });
}

const selected = flows.filter((flow) => FLOW_FILTER.test(flow.name));
if (selected.length === 0) {
  console.error(red(`soak: no flows match ${FLOW_FILTER}`));
  process.exit(2);
}

/** @type {{ name: string, hardFails: string[], softFails: string[], error?: string }[]} */
const outcomes = [];
for (const flow of selected) {
  log("soak", bold(`=== flow ${flow.name} ===`));
  try {
    let result;
    if (flow.run) {
      result = await flow.run(socket, (message) => log(flow.name, message));
    } else {
      await clearChat();
      result = await runFlow(flow);
    }
    const hardFails = flow.hard(result);
    const softFails = flow.soft(result);
    if (result.chatError) hardFails.unshift(`chat-error during flow: ${result.chatError}`);
    for (const problem of hardFails) log(flow.name, `${red("FAIL")} ${problem}`);
    for (const problem of softFails) log(flow.name, `${yellow("WARN")} ${problem}`);
    if (hardFails.length === 0) log(flow.name, green("PASS (all hard assertions)"));
    outcomes.push({ name: flow.name, hardFails, softFails });
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    log(flow.name, `${red("FAIL")} ${message}`);
    outcomes.push({ name: flow.name, hardFails: [message], softFails: [] });
  }
}

console.log("");
console.log(bold("soak summary"));
for (const outcome of outcomes) {
  const status = outcome.hardFails.length === 0 ? green("PASS") : red("FAIL");
  const warns = outcome.softFails.length > 0 ? yellow(` (${outcome.softFails.length} warn)`) : "";
  console.log(`  ${status} ${outcome.name}${warns}`);
}
const failed = outcomes.filter((outcome) => outcome.hardFails.length > 0).length;
console.log(failed === 0 ? green("soak: OK") : red(`soak: ${failed} flow(s) failed`));
socket.close();
process.exit(failed);
