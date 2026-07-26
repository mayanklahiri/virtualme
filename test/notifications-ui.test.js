// @ts-nocheck
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const root = new URL("../", import.meta.url);

test("notification route, controls, page, and safe renderer module exist", async () => {
  const [html, router, app, module, assets] = await Promise.all([
    readFile(new URL("controller/web/static/index.html", root), "utf8"),
    readFile(new URL("controller/web/static/js/router.js", root), "utf8"),
    readFile(new URL("controller/web/static/js/app.js", root), "utf8"),
    readFile(new URL("controller/web/static/js/notifications.js", root), "utf8"),
    readFile(new URL("controller/tools/fetch-assets.sh", root), "utf8"),
  ]);
  for (const id of [
    "notification-bell", "notification-popover", "notification-nav-badge",
    "notifications-read-all", "notifications-status", "notification-filters",
    "notification-list", "notification-detail", "notification-detail-curtain",
  ]) {
    assert.match(html, new RegExp(`id="${id}"`), id);
  }
  assert.match(router, /"\/notifications"/);
  assert.match(app, /initNotifications/);
  assert.doesNotMatch(module, /innerHTML/);
  for (const renderer of ["generic", "lifecycle", "configuration", "agent"]) {
    assert.match(module, new RegExp(`${renderer}:`));
  }
  for (const icon of ["bell", "circle-info", "circle-check", "settings"]) {
    assert.match(assets, new RegExp(icon));
  }
});

test("notification module keeps protocol and safety invariants explicit", async () => {
  const source = await readFile(new URL("controller/web/static/js/notifications.js", root), "utf8");
  for (const frame of [
    "notifications-req", "notification-read", "notifications-read-all",
    "notifications-page-req", "notification-detail-req", "notifications-state",
    "notifications-page", "notification-detail", "notification-error",
  ]) {
    assert.match(source, new RegExp(frame));
  }
  assert.match(source, /textContent/);
  assert.match(source, /99\+/);
  assert.match(source, /limit:\s*50/);
  assert.match(source, /slice\(0,\s*10\)/);
  assert.match(source, /Disconnected; notification state may be stale\./);
  assert.match(source, /Notification not found/);
});

class FakeClassList {
  constructor(node) {
    this.node = node;
    this.values = new Set();
  }
  add(...values) { values.forEach((value) => this.values.add(value)); }
  remove(...values) { values.forEach((value) => this.values.delete(value)); }
  contains(value) { return this.values.has(value); }
  toggle(value, force) {
    const enabled = force ?? !this.values.has(value);
    if (enabled) this.values.add(value);
    else this.values.delete(value);
    return enabled;
  }
}

class FakeElement {
  constructor(tagName, ownerDocument) {
    this.tagName = tagName.toUpperCase();
    this.ownerDocument = ownerDocument;
    this.children = [];
    this.attributes = new Map();
    this.listeners = new Map();
    this.classList = new FakeClassList(this);
    this.dataset = {};
    this.hidden = false;
    this.disabled = false;
    this._text = "";
  }
  set id(value) {
    this._id = value;
    this.ownerDocument.ids.set(value, this);
  }
  get id() { return this._id; }
  set className(value) {
    this.classList.values = new Set(String(value).split(/\s+/).filter(Boolean));
  }
  get className() { return [...this.classList.values].join(" "); }
  set textContent(value) {
    this._text = String(value ?? "");
    this.children = [];
  }
  get textContent() {
    return this._text + this.children.map((child) => child.textContent ?? "").join("");
  }
  append(...children) {
    for (const child of children.flat()) {
      if (child === null || child === undefined) continue;
      if (typeof child === "string") {
        const text = new FakeElement("#text", this.ownerDocument);
        text._text = child;
        this.children.push(text);
      } else {
        child.parentNode = this;
        this.children.push(child);
      }
    }
  }
  replaceChildren(...children) {
    this.children = [];
    this._text = "";
    this.append(...children);
  }
  setAttribute(name, value) { this.attributes.set(name, String(value)); }
  getAttribute(name) { return this.attributes.get(name) ?? null; }
  removeAttribute(name) { this.attributes.delete(name); }
  addEventListener(type, listener) {
    const listeners = this.listeners.get(type) ?? [];
    listeners.push(listener);
    this.listeners.set(type, listeners);
  }
  dispatch(type, init = {}) {
    const event = {
      type, target: this, currentTarget: this, key: "", shiftKey: false,
      preventDefault() { this.defaultPrevented = true; },
      ...init,
    };
    for (const listener of this.listeners.get(type) ?? []) listener(event);
    return event;
  }
  click() { this.dispatch("click"); }
  focus() { this.ownerDocument.activeElement = this; }
  contains(node) {
    return node === this || this.children.some((child) => child.contains?.(node));
  }
  descendants() {
    return this.children.flatMap((child) => [child, ...(child.descendants?.() ?? [])]);
  }
  matches(selector) {
    if (selector.startsWith(".")) return this.classList.contains(selector.slice(1));
    if (selector === "button") return this.tagName === "BUTTON";
    if (selector === "a") return this.tagName === "A";
    if (selector === "a[href]") return this.tagName === "A" && this.attributes.has("href");
    if (selector === "button:not([disabled])") return this.tagName === "BUTTON" && !this.disabled;
    return false;
  }
  querySelectorAll(selector) {
    const options = selector.split(",").map((value) => value.trim());
    return this.descendants().filter((node) => options.some((option) => node.matches?.(option)));
  }
  querySelector(selector) { return this.querySelectorAll(selector)[0] ?? null; }
}

class FakeDocument {
  constructor() {
    this.ids = new Map();
    this.listeners = new Map();
    this.documentElement = new FakeElement("html", this);
    this.body = new FakeElement("body", this);
    this.documentElement.append(this.body);
    this.activeElement = this.body;
  }
  createElement(name) { return new FakeElement(name, this); }
  createElementNS(_namespace, name) { return this.createElement(name); }
  createTextNode(text) {
    const node = this.createElement("#text");
    node.textContent = text;
    return node;
  }
  querySelector(selector) {
    if (selector.startsWith("#") && !selector.includes(" ")) return this.ids.get(selector.slice(1)) ?? null;
    return this.querySelectorAll(selector)[0] ?? null;
  }
  querySelectorAll(selector) {
    const match = selector.match(/^#([^ ]+) (.+)$/);
    if (match) return this.ids.get(match[1])?.querySelectorAll(match[2]) ?? [];
    return this.body.querySelectorAll(selector);
  }
  addEventListener(type, listener) {
    const listeners = this.listeners.get(type) ?? [];
    listeners.push(listener);
    this.listeners.set(type, listeners);
  }
  dispatch(type, init = {}) {
    const event = {
      type, target: this, key: "", shiftKey: false,
      preventDefault() { this.defaultPrevented = true; },
      ...init,
    };
    for (const listener of this.listeners.get(type) ?? []) listener(event);
  }
}

function notificationDOM() {
  const document = new FakeDocument();
  const make = (id, tag = "div", parent = document.body) => {
    const node = document.createElement(tag);
    node.id = id;
    parent.append(node);
    return node;
  };
  const bell = make("notification-bell", "button");
  make("notification-bell-badge", "span", bell);
  make("notification-nav-badge", "span");
  const popover = make("notification-popover");
  popover.hidden = true;
  make("notification-popover-list", "div", popover);
  const actions = document.createElement("div");
  popover.append(actions);
  make("notification-popover-read-all", "button", actions);
  const all = make("notification-view-all", "a", actions);
  all.setAttribute("href", "/notifications");
  make("notifications-read-all", "button");
  make("notifications-status");
  make("notification-filters");
  make("notification-list", "ol");
  make("notification-list-count");
  make("notification-load-older", "button");
  make("notification-detail", "aside");
  const curtain = make("notification-detail-curtain");
  curtain.hidden = true;
  make("theme-button", "button");
  make("theme-current", "span");
  const themePopover = make("theme-popover");
  themePopover.hidden = true;
  make("theme-options", "div", themePopover);
  make("variant-options", "div", themePopover);
  return document;
}

test("notification UI executes protocol, focus, history, reconnect, and safe rendering", async () => {
  const document = notificationDOM();
  const globalListeners = new Map();
  let mobile = false;
  const location = { origin: "http://example.test", pathname: "/notifications", href: "http://example.test/notifications" };
  const setURL = (value) => {
    const url = new URL(value, location.href);
    location.pathname = url.pathname;
    location.href = url.href;
  };
  globalThis.document = document;
  globalThis.location = location;
  globalThis.history = {
    pushState(_state, _unused, value) { setURL(value); },
    replaceState(_state, _unused, value) { setURL(value); },
  };
  globalThis.matchMedia = () => ({ matches: mobile, addEventListener() {} });
  globalThis.localStorage = { getItem() { return null; }, setItem() {} };
  globalThis.requestAnimationFrame = (callback) => callback();
  globalThis.CSS = { escape: (value) => value };
  globalThis.CustomEvent = class { constructor(type) { this.type = type; } };
  globalThis.PopStateEvent = class { constructor(type) { this.type = type; } };
  globalThis.addEventListener = (type, listener) => {
    const listeners = globalListeners.get(type) ?? [];
    listeners.push(listener);
    globalListeners.set(type, listeners);
  };
  globalThis.dispatchEvent = (event) => {
    for (const listener of globalListeners.get(event.type) ?? []) listener(event);
  };

  const module = await import(`../controller/web/static/js/notifications.js?test=${Date.now()}`);
  const { initTheme } = await import(`../controller/web/static/js/theme.js?test=${Date.now()}`);
  initTheme();
  assert.deepEqual(Object.keys(module.sortedDetailData({ z: 1, a: 2 })), ["a", "z"]);
  const sent = [];
  const ui = module.initNotifications((frame) => sent.push(frame));
  ui.status("connected");
  assert.deepEqual(sent[0], { type: "notifications-req" });
  const pageRequest = sent.find((frame) => frame.type === "notifications-page-req");
  assert.equal(pageRequest.before, "");
  assert.equal(pageRequest.limit, 50);

  const malicious = `<img src=x onerror=alert(1)>`;
  ui.frame({
    type: "notifications-state", unread: 1, retainedCount: 2,
    types: [{ name: "info", icon: "i-circle-info", defaultRenderer: "generic" }],
    notifications: [
      { id: "01ARZ3NDEKTSV4RRFFQ69G5FAV", type: "info", sender: "agent",
        title: malicious, summary: "unsafe javascript:alert(1)", occurredAtMs: 1, createdAtMs: 2,
        renderer: "generic" },
      { id: "01ARZ3NDEKTSV4RRFFQ69G5FAW", type: "info", sender: "agent",
        title: "Read", summary: "done", occurredAtMs: 1, createdAtMs: 1, readAtMs: 2,
        renderer: "generic" },
    ],
  });
  ui.frame({
    type: "notifications-page", requestId: pageRequest.requestId, done: true,
    nextBefore: "", retainedCount: 2,
    notifications: [
      { id: "01ARZ3NDEKTSV4RRFFQ69G5FAV", type: "info", sender: "agent",
        title: malicious, summary: "unsafe javascript:alert(1)", occurredAtMs: 1, createdAtMs: 2,
        renderer: "generic" },
      { id: "01ARZ3NDEKTSV4RRFFQ69G5FAW", type: "info", sender: "agent",
        title: "Read", summary: "done", occurredAtMs: 1, createdAtMs: 1, readAtMs: 2,
        renderer: "generic" },
    ],
  });
  assert.match(document.querySelector("#notification-list").textContent, /<img src=x/);
  assert.equal(document.querySelector("#notification-list").children.length, 2);
  const listRows = document.querySelectorAll("#notification-list .notification-row");
  listRows[0].dispatch("keydown", { key: "ArrowDown" });
  assert.equal(document.activeElement, listRows[1]);

  mobile = true;
  listRows[0].click();
  assert.equal(document.querySelector("#notification-detail").classList.contains("open"), true);
  assert.equal(document.querySelector("#notification-detail-curtain").hidden, false);
  const selectedRequest = sent.findLast((frame) => frame.type === "notification-detail-req");
  ui.frame({
    type: "notification-detail", requestId: selectedRequest.requestId,
    notification: {
      id: selectedRequest.id, type: "info", sender: "agent", title: malicious,
      summary: "unsafe javascript:alert(1)", occurredAtMs: 1, createdAtMs: 2,
      detail: { renderer: "future", data: { value: "javascript:alert(1)" } },
    },
  });
  assert.match(document.querySelector("#notification-detail").textContent, /Unsupported detail renderer: future/);
  document.querySelector("#notification-detail-curtain").click();
  assert.equal(document.querySelector("#notification-detail").classList.contains("open"), false);
  mobile = false;

  document.querySelector("#notification-bell").click();
  assert.equal(document.querySelector("#notification-popover").hidden, false);
  assert.equal(document.activeElement.dataset.id, "01ARZ3NDEKTSV4RRFFQ69G5FAV");
  const popoverFocus = document.querySelector("#notification-popover").querySelectorAll("a[href],button:not([disabled])");
  popoverFocus.at(-1).focus();
  document.dispatch("keydown", { key: "Tab" });
  assert.equal(document.activeElement, popoverFocus[0]);
  document.dispatch("keydown", { key: "Escape" });
  assert.equal(document.querySelector("#notification-popover").hidden, true);
  assert.equal(document.activeElement, document.querySelector("#notification-bell"));
  document.querySelector("#theme-button").click();
  assert.equal(document.querySelector("#theme-popover").hidden, false);
  document.querySelector("#notification-bell").click();
  assert.equal(document.querySelector("#theme-popover").hidden, true);
  assert.equal(document.querySelector("#notification-popover").hidden, false);
  document.querySelector("#theme-button").click();
  assert.equal(document.querySelector("#notification-popover").hidden, true);

  setURL("/notifications?id=01ARZ3NDEKTSV4RRFFQ69G5FAX");
  ui.enter();
  const deepRequest = sent.findLast((frame) => frame.type === "notification-detail-req");
  assert.equal(deepRequest.id, "01ARZ3NDEKTSV4RRFFQ69G5FAX");
  ui.frame({ type: "notification-error", requestId: "stale", code: "not_found", error: "stale" });
  assert.doesNotMatch(document.querySelector("#notifications-status").textContent, /stale/);
  ui.frame({ type: "notification-error", requestId: deepRequest.requestId, code: "not_found", error: "missing" });
  assert.equal(document.querySelector("#notification-detail").textContent, "Notification not found");

  setURL("/notifications");
  globalThis.dispatchEvent(new globalThis.PopStateEvent("popstate"));
  assert.equal(document.querySelector("#notification-detail").textContent, "Select a notification to inspect it.");

  const beforeCreated = sent.length;
  const pendingPage = sent.findLast((frame) => frame.type === "notifications-page-req");
  ui.frame({
    type: "notifications-state", unread: 2, retainedCount: 3,
    types: [{ name: "info", icon: "i-circle-info", defaultRenderer: "generic" }],
    change: { kind: "created", id: "01ARZ3NDEKTSV4RRFFQ69G5FAY", readAtMs: 0 },
    notifications: [],
  });
  ui.frame({
    type: "notifications-page", requestId: pendingPage.requestId, done: true,
    nextBefore: "", retainedCount: 3, notifications: [],
  });
  assert.ok(sent.slice(beforeCreated).some((frame) => frame.type === "notifications-page-req" && frame.before === ""));

  mobile = true;
  ui.status("disconnected");
  assert.match(document.querySelector("#notifications-status").textContent, /Disconnected/);
  ui.status("connected");
  assert.ok(sent.filter((frame) => frame.type === "notifications-req").length >= 2);
});
