// @ts-nocheck
import { readFileSync } from "node:fs";
import path from "node:path";
import vm from "node:vm";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const readPagePath = path.join(root, "controller/internal/agent/js/readpage.js");

/** @param {any} fixture */
export function createDOMStub(fixture) {
  /** @param {any} node @param {any} parent */
  function build(node, parent) {
    if (node.tag === "#text") {
      return { nodeType: 3, nodeValue: node.text ?? "", parentElement: parent };
    }
    const el = {
      nodeType: 1,
      tagName: node.tag.toUpperCase(),
      id: node.id ?? "",
      parentElement: parent,
      childNodes: [],
      children: [],
      getAttribute(name) {
        return node.attrs?.[name] ?? null;
      },
      getClientRects() {
        return node.visible === false ? [] : [{ x: 0, y: 0, width: 10, height: 10 }];
      },
    };
    if (node.id) el.id = node.id;
    else el.id = "";
    for (const child of node.children ?? []) {
      const built = build(child, el);
      el.childNodes.push(built);
      if (built.nodeType === 1) el.children.push(built);
    }
    if (node.text) {
      el.childNodes.unshift({ nodeType: 3, nodeValue: node.text, parentElement: el });
    }
    return el;
  }

  const headNodes = (fixture.head ?? []).map((node) => build(node, null));
  const bodyNodes = (fixture.body ?? []).map((node) => build(node, null));
  const headEl = {
    nodeType: 1,
    tagName: "HEAD",
    children: headNodes,
    childNodes: headNodes,
    getElementsByTagName(tag) {
      const out = [];
      const walk = (nodes) => {
        for (const node of nodes) {
          if (node.nodeType !== 1) continue;
          if (node.tagName.toLowerCase() === tag.toLowerCase()) out.push(node);
          walk(node.children ?? []);
        }
      };
      walk(headNodes);
      return out;
    },
    getAttribute() { return null; },
  };
  for (const node of headNodes) node.parentElement = headEl;

  const bodyEl = {
    nodeType: 1,
    tagName: "BODY",
    children: bodyNodes,
    childNodes: bodyNodes,
    parentElement: null,
    getElementsByTagName() { return []; },
    getAttribute() { return null; },
  };
  for (const node of bodyNodes) node.parentElement = bodyEl;

  const documentElement = {
    nodeType: 1,
    tagName: "HTML",
    getAttribute(name) {
      return name === "lang" ? fixture.lang ?? "en" : null;
    },
  };

  const document = {
    title: fixture.title ?? "",
    documentElement,
    head: headEl,
    body: bodyEl,
    getElementsByTagName(tag) {
      if (tag.toLowerCase() === "label") return [];
      return [];
    },
  };

  const location = { href: fixture.url ?? "https://example.com/" };

  function querySelectorAll(selector) {
    const matches = [];
    const walk = (nodes) => {
      for (const node of nodes) {
        if (node.nodeType !== 1) continue;
        if (selectorMatches(node, selector)) matches.push(node);
        walk(node.children ?? []);
      }
    };
    walk(bodyNodes);
    return matches;
  }

  /** @param {any} el @param {string} selector */
  function selectorMatches(el, selector) {
    if (selector.startsWith("#")) {
      return el.id === selector.slice(1);
    }
    const tagMatch = selector.match(/^([a-z0-9-]+)$/i);
    if (tagMatch) return el.tagName.toLowerCase() === tagMatch[1].toLowerCase();
    return el.tagName.toLowerCase() === "a" && selector === "a";
  }

  document.querySelectorAll = querySelectorAll;

  return {
    document,
    location,
    window: {
      getComputedStyle() {
        return { visibility: "visible", display: "block" };
      },
    },
  };
}

/** @param {any} fixture */
export function runReadPage(fixture) {
  const sandbox = createDOMStub(fixture);
  const context = vm.createContext({
    ...sandbox,
    console,
    URL,
  });
  // Structured-clone out of the vm realm so assert.deepEqual works.
  return JSON.parse(JSON.stringify(vm.runInContext(readFileSync(readPagePath, "utf8"), context)));
}
