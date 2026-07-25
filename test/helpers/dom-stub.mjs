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
      innerText: node.text ?? "",
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

  /** @param {(el: any) => boolean} predicate */
  function findAll(predicate) {
    const matches = [];
    const walk = (nodes) => {
      for (const node of nodes) {
        if (node.nodeType !== 1) continue;
        if (predicate(node)) matches.push(node);
        walk(node.children ?? []);
      }
    };
    walk(bodyNodes);
    return matches;
  }

  // Supports the selector subset the agent tools emit: bare tag, #id, and
  // `body > tag:nth-of-type(k)` paths with #id restarts (spec 027 §3e).
  function querySelectorAll(selector) {
    if (!selector.includes(" > ")) {
      if (selector.startsWith("#")) {
        const id = selector.slice(1).replace(/\\(.)/g, "$1");
        return findAll((el) => el.id === id);
      }
      const tag = selector.toLowerCase();
      return findAll((el) => el.tagName.toLowerCase() === tag);
    }
    const segments = selector.split(" > ").map((part) => part.trim());
    /** @type {any[]} */
    let contexts;
    let start = 0;
    if (segments[0] === "body") {
      contexts = [bodyEl];
      start = 1;
    } else if (segments[0].startsWith("#")) {
      const id = segments[0].slice(1).replace(/\\(.)/g, "$1");
      contexts = findAll((el) => el.id === id);
      start = 1;
    } else {
      contexts = [bodyEl];
    }
    for (let index = start; index < segments.length; index++) {
      const segment = segments[index];
      /** @type {any[]} */
      const next = [];
      for (const context of contexts) {
        if (segment.startsWith("#")) {
          const id = segment.slice(1).replace(/\\(.)/g, "$1");
          for (const child of context.children ?? []) {
            if (child.id === id) next.push(child);
          }
          continue;
        }
        const match = segment.match(/^([a-z0-9-]+)(?::nth-of-type\((\d+)\))?$/i);
        if (!match) return [];
        const tag = match[1].toLowerCase();
        const nth = match[2] ? Number(match[2]) : 0;
        let count = 0;
        for (const child of context.children ?? []) {
          if (child.tagName.toLowerCase() !== tag) continue;
          count++;
          if (!nth) next.push(child);
          else if (count === nth) {
            next.push(child);
            break;
          }
        }
      }
      contexts = next;
    }
    return contexts;
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
