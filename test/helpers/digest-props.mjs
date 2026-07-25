// @ts-nocheck
import assert from "node:assert/strict";
import { Buffer } from "node:buffer";

function valueMatches(actual, expected) {
  if (expected instanceof RegExp) return expected.test(String(actual ?? ""));
  if (typeof expected === "function") return Boolean(expected(actual));
  return actual === expected;
}

export function matches(node, matcher) {
  if (typeof matcher === "function") return Boolean(matcher(node));
  for (const [key, expected] of Object.entries(matcher ?? {})) {
    if (key === "descendant") {
      if (!walkNodes(node.children ?? []).some((child) => matches(child, expected))) return false;
    } else if (!valueMatches(node?.[key], expected)) {
      return false;
    }
  }
  return true;
}

export function walkNodes(input) {
  const result = [];
  const visit = (value) => {
    if (Array.isArray(value)) {
      for (const item of value) visit(item);
      return;
    }
    if (!value || typeof value !== "object") return;
    result.push(value);
    visit(value.children);
    visit(value.items);
    visit(value.rows);
  };
  visit(input);
  return result;
}

function property(name, check) {
  return { name, check };
}

export function count(matcher, bounds) {
  return property(`count ${JSON.stringify(bounds)}`, ({ nodes }) => {
    const actual = nodes.filter((node) => matches(node, matcher)).length;
    if (bounds.eq !== undefined) assert.equal(actual, bounds.eq);
    if (bounds.gte !== undefined) assert.ok(actual >= bounds.gte, `${actual} < ${bounds.gte}`);
    if (bounds.lte !== undefined) assert.ok(actual <= bounds.lte, `${actual} > ${bounds.lte}`);
  });
}

export function forAll(matcher, required) {
  return property("all matching nodes have required fields", ({ nodes }) => {
    const selected = nodes.filter((node) => matches(node, matcher));
    assert.ok(selected.length > 0, "matcher selected no nodes");
    for (const node of selected) {
      if (typeof required === "function") assert.ok(required(node), JSON.stringify(node));
      else for (const key of required) assert.ok(node[key] !== undefined && node[key] !== "", `${key}: ${JSON.stringify(node)}`);
    }
  });
}

export function exists(matcher) {
  return property("matching node exists", ({ nodes }) => {
    assert.ok(nodes.some((node) => matches(node, matcher)), `no match for ${String(matcher)}`);
  });
}

export function textMatches(pattern, bounds = { gte: 1 }) {
  return count({ text: pattern }, bounds);
}

export function noTruncationMarkers() {
  return property("digest has no truncation markers", ({ yaml }) => {
    assert.doesNotMatch(yaml, /(?:…truncated|truncated:)/i);
  });
}

export function yamlUnder(bytes) {
  return property(`YAML is at most ${bytes} bytes`, ({ yaml }) => {
    assert.ok(Buffer.byteLength(yaml) <= bytes, `${Buffer.byteLength(yaml)} > ${bytes}`);
  });
}

export function custom(name, check) {
  return property(name, check);
}

export function evaluateProperties(properties, digest, yaml) {
  const context = { digest, yaml, nodes: walkNodes(digest.body ?? []) };
  for (const item of properties) {
    try {
      item.check(context);
    } catch (error) {
      error.message = `${item.name}: ${error.message}`;
      throw error;
    }
  }
}
