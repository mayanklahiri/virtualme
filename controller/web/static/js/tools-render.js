// Pure, shape-based classification of tool results (no DOM, no tool names).

/**
 * @typedef {{kind: "yaml-digest", url: string, title: string, head: any, body: any}
 *   | {kind: "page", url: string, title: string, text: string}
 *   | {kind: "json"} | {kind: "text"}} ResultShape
 */

import { parseYamlLite } from "./yaml-lite.js";

/**
 * Classify a tool result payload by shape. A "page" is JSON whose keys are a
 * non-empty subset of {url, title, text} with string values and a valid
 * http(s) url. A yaml-digest is subset YAML with title, url, head, body.
 * @param {string} payload
 * @returns {ResultShape}
 */
export function classifyResult(payload) {
  try {
    const parsed = parseYamlLite(payload);
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed) &&
        typeof parsed.title === "string" && typeof parsed.url === "string" &&
        /^https?:\/\//.test(parsed.url)) {
      return {
        kind: "yaml-digest",
        url: parsed.url,
        title: parsed.title,
        head: parsed.head ?? {},
        body: parsed.body ?? [],
      };
    }
  } catch {
    // fall through to JSON/text classification
  }
  let parsed;
  try {
    parsed = JSON.parse(payload);
  } catch {
    return { kind: "text" };
  }
  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
    return { kind: "json" };
  }
  const keys = Object.keys(parsed);
  const allowed = new Set(["url", "title", "text"]);
  const pageShaped = keys.length > 0 &&
    keys.every((key) => allowed.has(key) && typeof parsed[key] === "string");
  if (!pageShaped || !/^https?:\/\//.test(String(parsed.url ?? ""))) {
    return { kind: "json" };
  }
  return {
    kind: "page",
    url: String(parsed.url ?? ""),
    title: String(parsed.title ?? ""),
    text: String(parsed.text ?? ""),
  };
}

const envLine = /^[A-Za-z_][A-Za-z0-9_]*=/;

/**
 * @typedef {{kind: "env", entries: Array<[string, string]>}
 *   | {kind: "text", text: string}} EnvSegment
 */

/**
 * Split plain text into segments; any run of three or more consecutive
 * KEY=value lines becomes an env table with entries sorted by key.
 * @param {string} text
 * @returns {EnvSegment[]}
 */
export function parseEnvBlocks(text) {
  const lines = String(text ?? "").split("\n");
  /** @type {EnvSegment[]} */
  const segments = [];
  /** @type {string[]} */
  let textRun = [];
  /** @type {string[]} */
  let envRun = [];

  function flushText() {
    if (textRun.length > 0) {
      segments.push({ kind: "text", text: textRun.join("\n") });
      textRun = [];
    }
  }
  function flushEnv() {
    if (envRun.length >= 3) {
      flushText();
      const entries = envRun.map((line) => {
        const index = line.indexOf("=");
        return /** @type {[string, string]} */ ([line.slice(0, index), line.slice(index + 1)]);
      });
      entries.sort((a, b) => a[0].localeCompare(b[0]));
      segments.push({ kind: "env", entries });
    } else {
      textRun.push(...envRun);
    }
    envRun = [];
  }

  for (const line of lines) {
    if (envLine.test(line)) {
      envRun.push(line);
    } else {
      flushEnv();
      textRun.push(line);
    }
  }
  flushEnv();
  flushText();
  return segments;
}
