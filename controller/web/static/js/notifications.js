import { renderTree } from "./tree.js";

const knownIcons = new Set([
  "i-circle-info", "i-circle-check", "i-triangle-alert", "i-circle-x",
  "i-bot", "i-monitor", "i-settings", "i-bell",
]);

const state = {
  connected: false, ready: false, recent: [], loaded: [], types: [],
  unread: 0, retained: 0, nextBefore: "", done: true, selected: null,
  detail: null, typeFilter: "all", readFilter: "all", pageRequest: "",
  detailRequest: "", error: "",
};

let sendFrame = () => {};
let requestSequence = 0;
const requestID = (prefix) => `${prefix}:${Date.now()}:${++requestSequence}`;

function element(name, className, text) {
  const node = document.createElement(name);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function icon(name) {
  const svg = document.createElementNS("http:" + "//www.w3.org/2000/svg", "svg");
  svg.classList.add("icon");
  svg.setAttribute("aria-hidden", "true");
  const use = document.createElementNS("http:" + "//www.w3.org/2000/svg", "use");
  use.setAttribute("href", `/icons.svg#${knownIcons.has(name) ? name : "i-circle-info"}`);
  svg.append(use);
  return svg;
}

function typeIcon(notification) {
  const definition = state.types.find(({ name }) => name === notification.type);
  return icon(knownIcons.has(definition?.icon) ? definition.icon : "i-circle-info");
}

function formatTime(milliseconds) {
  const date = new Date(milliseconds);
  const age = Date.now() - milliseconds;
  if (age >= 0 && age < 60_000) return "just now";
  if (age >= 0 && age < 3_600_000) return `${Math.floor(age / 60_000)}m ago`;
  if (age >= 0 && age < 86_400_000) return `${Math.floor(age / 3_600_000)}h ago`;
  return date.toLocaleString();
}

function appendSummary(container, notification, emphasis = "") {
  container.append(typeIcon(notification));
  const copy = element("span", "notification-copy");
  const title = element("strong", "", notification.title);
  if (!notification.readAtMs) title.classList.add("notification-unread-title");
  const summary = element("span", "notification-summary", notification.summary);
  const metadata = element("span", "notification-meta");
  metadata.append(element("span", "", notification.sender));
  if (notification.subtype) metadata.append(element("span", "", emphasis || notification.subtype));
  const time = element("time", "", formatTime(notification.occurredAtMs));
  time.dateTime = new Date(notification.occurredAtMs).toISOString();
  metadata.append(time);
  copy.append(title, summary, metadata);
  container.append(copy);
  if (!notification.readAtMs) container.append(element("span", "notification-unread-dot", "Unread"));
}

function renderGenericPopover(container, notification) { appendSummary(container, notification); }
function renderLifecyclePopover(container, notification) { appendSummary(container, notification, notification.subtype?.replaceAll("-", " ")); }
function renderConfigurationPopover(container, notification) { appendSummary(container, notification, "saved config"); }
function renderAgentPopover(container, notification) { appendSummary(container, notification, notification.subtype || "agent"); }

function detailHeading(container, notification) {
  const heading = element("header", "notification-detail-heading");
  const back = element("button", "notification-detail-back", "Back");
  back.type = "button";
  back.addEventListener("click", closeMobileDetail);
  const copy = element("div");
  copy.append(element("h2", "", notification.title), element("p", "", notification.summary));
  heading.append(back, typeIcon(notification), copy);
  container.append(heading);
  const metadata = element("dl", "notification-detail-metadata");
  addDefinition(metadata, "Sender", notification.sender);
  addDefinition(metadata, "Type", notification.type);
  if (notification.subtype) addDefinition(metadata, "Subtype", notification.subtype);
  addDefinition(metadata, "Occurred", new Date(notification.occurredAtMs).toLocaleString());
  addDefinition(metadata, "Created", new Date(notification.createdAtMs).toLocaleString());
  addDefinition(metadata, "Read", notification.readAtMs ? new Date(notification.readAtMs).toLocaleString() : "Unread");
  container.append(metadata);
}

function addDefinition(list, label, value) {
  list.append(element("dt", "", label), element("dd", "", String(value)));
}

function renderGenericDetail(container, notification) {
  detailHeading(container, notification);
  const keys = Object.keys(notification.detail?.data ?? {}).sort();
  if (keys.length) container.append(renderTree(notification.detail.data, { expandDepth: 3 }));
}

function renderLifecycleDetail(container, notification) {
  detailHeading(container, notification);
  const labels = {
    runId: "Run ID", reason: "Reason", startedAtMs: "Started",
    shutdownAtMs: "Event time", previousRunId: "Previous run",
    previousStartedAtMs: "Previous start", lastMarkerAtMs: "Last marker",
    markerStatus: "Recovery status", recoveredFrom: "Recovered from",
  };
  const list = element("dl", "notification-detail-metadata");
  for (const [key, label] of Object.entries(labels)) {
    const value = notification.detail?.data?.[key];
    if (value !== undefined) {
      addDefinition(list, label, key.endsWith("AtMs") ? new Date(value).toLocaleString() : value);
    }
  }
  container.append(list);
}

function renderConfigurationDetail(container, notification) {
  detailHeading(container, notification);
  const data = notification.detail?.data ?? {};
  const list = element("dl", "notification-detail-metadata");
  addDefinition(list, "Restart required", data.restartRequired ? "Yes" : "No");
  if (data.revision) addDefinition(list, "Revision", data.revision);
  if (Array.isArray(data.changedKeys)) addDefinition(list, "Changed keys", data.changedKeys.join(", ") || "None");
  container.append(list);
}

function renderAgentDetail(container, notification) {
  detailHeading(container, notification);
  if (notification.subtype) container.append(element("p", "notification-agent-subtype", notification.subtype));
  container.append(renderTree(notification.detail?.data ?? {}, { expandDepth: 3 }));
}

const renderers = Object.freeze({
  generic: { popover: renderGenericPopover, detail: renderGenericDetail },
  lifecycle: { popover: renderLifecyclePopover, detail: renderLifecycleDetail },
  configuration: { popover: renderConfigurationPopover, detail: renderConfigurationDetail },
  agent: { popover: renderAgentPopover, detail: renderAgentDetail },
});

function rendererFor(notification) {
  return renderers[notification.renderer ?? notification.detail?.renderer] ?? renderers.generic;
}

export function safeLink(value) {
  try {
    const url = new URL(value, location.origin);
    if (url.protocol !== "http:" && url.protocol !== "https:") return null;
    if (String(value).startsWith("/") && url.origin !== location.origin) return null;
    return url;
  } catch {
    return null;
  }
}

function setBadge(node, count) {
  node.hidden = count === 0;
  node.textContent = count >= 100 ? "99+" : String(count);
  node.setAttribute("aria-label", `${count} unread notifications`);
}

function updateBadges() {
  setBadge(document.querySelector("#notification-bell-badge"), state.unread);
  setBadge(document.querySelector("#notification-nav-badge"), state.unread);
  for (const button of [
    document.querySelector("#notifications-read-all"),
    document.querySelector("#notification-popover-read-all"),
  ]) button.disabled = !state.connected || state.unread === 0;
}

function sorted(items) {
  return [...items].sort((a, b) => b.createdAtMs - a.createdAtMs || b.id.localeCompare(a.id));
}

function visibleItems() {
  return sorted(state.loaded).filter((item) =>
    (state.typeFilter === "all" || item.type === state.typeFilter) &&
    (state.readFilter === "all" || (state.readFilter === "unread") === !item.readAtMs));
}

function renderPopover() {
  const box = document.querySelector("#notification-popover-list");
  box.replaceChildren();
  const items = sorted(state.recent).slice(0, 10);
  if (!state.ready) {
    box.append(element("p", "notification-empty", "Loading notifications…"));
  } else if (!items.length) {
    box.append(element("p", "notification-empty", "No recent notifications."));
  } else {
    for (const notification of items) {
      const row = element("button", "notification-row notification-popover-row");
      row.type = "button";
      row.dataset.id = notification.id;
      rendererFor(notification).popover(row, notification);
      row.addEventListener("click", () => selectFromPopover(notification));
      box.append(row);
    }
  }
}

function renderFilters() {
  const box = document.querySelector("#notification-filters");
  box.replaceChildren();
  const typeGroup = element("div", "notification-filter-group");
  for (const type of [{ name: "all" }, ...state.types]) {
    const count = state.loaded.filter((item) => type.name === "all" || item.type === type.name).length;
    const button = element("button", "", `${type.name === "all" ? "All" : type.name} (${count})`);
    button.type = "button";
    button.setAttribute("aria-pressed", String(state.typeFilter === type.name));
    button.addEventListener("click", () => {
      state.typeFilter = type.name;
      renderPage();
      chooseFirstVisible();
    });
    typeGroup.append(button);
  }
  const readGroup = element("div", "notification-filter-group");
  for (const filter of ["all", "unread", "read"]) {
    const button = element("button", "", filter[0].toUpperCase() + filter.slice(1));
    button.type = "button";
    button.setAttribute("aria-pressed", String(state.readFilter === filter));
    button.addEventListener("click", () => {
      state.readFilter = filter;
      renderPage();
      chooseFirstVisible();
    });
    readGroup.append(button);
  }
  box.append(typeGroup, readGroup);
}

function renderPage() {
  renderFilters();
  const list = document.querySelector("#notification-list");
  list.replaceChildren();
  const items = visibleItems();
  if (!state.ready && !state.loaded.length) {
    for (let index = 0; index < 3; index++) list.append(element("li", "notification-skeleton", "Loading notifications…"));
  } else if (!state.loaded.length) {
    list.append(element("li", "notification-empty", "No notifications yet."));
  } else if (!items.length) {
    list.append(element("li", "notification-empty", "No notifications match these filters."));
  } else {
    for (const notification of items) {
      const item = element("li");
      const row = element("button", "notification-row");
      row.type = "button";
      row.dataset.id = notification.id;
      row.setAttribute("aria-current", String(state.selected === notification.id));
      rendererFor(notification).popover(row, notification);
      row.addEventListener("click", () => select(notification.id));
      row.addEventListener("keydown", listKeydown);
      item.append(row);
      list.append(item);
    }
  }
  const count = document.querySelector("#notification-list-count");
  count.textContent = state.done
    ? `Showing ${state.loaded.length} retained notifications.`
    : `Showing ${state.loaded.length} of ${state.retained} retained notifications. Filter applies to loaded notifications`;
  const load = document.querySelector("#notification-load-older");
  load.hidden = state.done;
  load.disabled = !state.connected || Boolean(state.pageRequest);
  renderDetail();
}

function renderDetail() {
  const container = document.querySelector("#notification-detail");
  container.replaceChildren();
  if (!state.selected) {
    container.textContent = state.error === "not_found" ? "Notification not found" : "Select a notification to inspect it.";
    return;
  }
  if (!state.detail || state.detail.id !== state.selected) {
    container.textContent = "Loading notification details…";
    return;
  }
  const rendererName = state.detail.detail?.renderer;
  rendererFor(state.detail).detail(container, state.detail);
  if (!renderers[rendererName]) {
    container.append(element("p", "notification-error", `Unsupported detail renderer: ${rendererName}`));
  }
}

function renderStatus() {
  const status = document.querySelector("#notifications-status");
  status.textContent = !state.connected
    ? "Disconnected; notification state may be stale."
    : state.error && state.error !== "not_found" ? state.error : "";
  status.classList.toggle("notification-error", Boolean(state.error) || !state.connected);
}

function render() {
  updateBadges();
  renderPopover();
  renderPage();
  renderStatus();
}

function sendRead(id) {
  const notification = [...state.recent, ...state.loaded].find((item) => item.id === id);
  if (state.connected && notification && !notification.readAtMs) {
    sendFrame({ type: "notification-read", requestId: requestID("read"), id });
  }
}

function select(id) {
  if (!id) return;
  state.selected = id;
  state.detail = state.detail?.id === id ? state.detail : null;
  state.error = "";
  renderPage();
  if (!state.connected) return;
  state.detailRequest = requestID("detail");
  sendFrame({ type: "notification-detail-req", requestId: state.detailRequest, id });
  sendRead(id);
  if (matchMedia("(max-width: 47.999rem)").matches) openMobileDetail();
}

function selectFromPopover(notification) {
  sendRead(notification.id);
  closePopover(false);
  history.pushState(null, "", `/notifications?id=${encodeURIComponent(notification.id)}`);
  dispatchEvent(new PopStateEvent("popstate"));
}

function chooseFirstVisible() {
  const first = visibleItems()[0];
  if (first) select(first.id);
  else {
    state.selected = null;
    state.detail = null;
    renderDetail();
  }
}

function listKeydown(event) {
  const rows = [...document.querySelectorAll("#notification-list .notification-row")];
  const index = rows.indexOf(event.currentTarget);
  let next = -1;
  if (event.key === "ArrowDown") next = Math.min(rows.length - 1, index + 1);
  else if (event.key === "ArrowUp") next = Math.max(0, index - 1);
  else if (event.key === "Home") next = 0;
  else if (event.key === "End") next = rows.length - 1;
  else if (event.key === "Enter" || event.key === " ") {
    event.preventDefault();
    select(event.currentTarget.dataset.id);
    return;
  }
  if (next >= 0) {
    event.preventDefault();
    rows[next]?.focus();
  }
}

function loadPage(replace = false) {
  if (!state.connected || state.pageRequest) return;
  state.pageRequest = requestID("page");
  if (replace) {
    state.loaded = [];
    state.nextBefore = "";
  }
  sendFrame({
    type: "notifications-page-req", requestId: state.pageRequest,
    before: replace ? "" : state.nextBefore, limit: 50,
  });
  render();
}

function markAll() {
  if (!state.connected || !state.unread) return;
  sendFrame({ type: "notifications-read-all", requestId: requestID("read-all") });
}

function applyChange(update) {
  if (!update) return;
  const apply = (item) => {
    if (update.kind === "read" && item.id === update.id) item.readAtMs = update.readAtMs;
    if (update.kind === "read-all") item.readAtMs ||= update.readAtMs;
  };
  state.loaded.forEach(apply);
  if (state.detail) apply(state.detail);
}

function frame(message) {
  if (message.type === "notifications-state") {
    state.ready = true;
    state.recent = sorted(message.notifications ?? []);
    state.types = message.types ?? [];
    state.unread = Number(message.unread ?? 0);
    state.retained = Number(message.retainedCount ?? 0);
    applyChange(message.change);
    render();
  } else if (message.type === "notifications-page" && message.requestId === state.pageRequest) {
    const replacing = !state.nextBefore;
    state.loaded = sorted(replacing
      ? message.notifications ?? []
      : [...state.loaded, ...(message.notifications ?? [])].filter((item, index, all) =>
        all.findIndex(({ id }) => id === item.id) === index));
    state.nextBefore = message.nextBefore ?? "";
    state.done = Boolean(message.done);
    state.retained = Number(message.retainedCount ?? state.retained);
    state.pageRequest = "";
    const requested = new URL(location.href).searchParams.get("id");
    if (requested && state.loaded.some(({ id }) => id === requested)) select(requested);
    else if (!state.selected && !requested) chooseFirstVisible();
    render();
  } else if (message.type === "notification-detail" && message.requestId === state.detailRequest) {
    state.detailRequest = "";
    state.detail = message.notification;
    state.selected = message.notification.id;
    const url = new URL(location.href);
    url.searchParams.set("id", state.selected);
    history.replaceState(null, "", url.pathname + url.search);
    render();
  } else if (message.type === "notification-error") {
    if (message.requestId === state.pageRequest) {
      const expired = message.code === "not_found" && Boolean(state.nextBefore);
      state.pageRequest = "";
      state.error = message.error ?? "Notification request failed";
      if (expired) loadPage(true);
    } else if (message.requestId === state.detailRequest) {
      state.detailRequest = "";
      state.detail = null;
      state.selected = null;
      state.error = message.code === "not_found" ? "not_found" : (message.error ?? "Notification request failed");
    } else {
      state.error = message.error ?? "Notification request failed";
    }
    render();
  }
}

function closePopover(restore = true) {
  const popover = document.querySelector("#notification-popover");
  if (popover.hidden) return;
  const inside = popover.contains(document.activeElement);
  popover.hidden = true;
  document.querySelector("#notification-bell").setAttribute("aria-expanded", "false");
  if (restore && inside) document.querySelector("#notification-bell").focus();
}

function togglePopover() {
  const popover = document.querySelector("#notification-popover");
  const opening = popover.hidden;
  if (opening) {
    dispatchEvent(new CustomEvent("notificationpopoveropen"));
    popover.hidden = false;
    document.querySelector("#notification-bell").setAttribute("aria-expanded", "true");
    const rows = [...popover.querySelectorAll(".notification-row")];
    (rows.find((row) => row.querySelector(".notification-unread-dot")) ?? rows[0] ??
      popover.querySelector("a,button"))?.focus();
  } else closePopover();
}

function openMobileDetail() {
  document.querySelector("#notification-detail").classList.add("open");
  const curtain = document.querySelector("#notification-detail-curtain");
  curtain.hidden = false;
  requestAnimationFrame(() => curtain.classList.add("open"));
  document.body.classList.add("notification-detail-locked");
}

function closeMobileDetail() {
  document.querySelector("#notification-detail").classList.remove("open");
  const curtain = document.querySelector("#notification-detail-curtain");
  curtain.classList.remove("open");
  curtain.hidden = true;
  document.body.classList.remove("notification-detail-locked");
  document.querySelector(`#notification-list [data-id="${CSS.escape(state.selected ?? "")}"]`)?.focus();
}

function status(connection) {
  state.connected = connection === "connected";
  if (!state.connected) closePopover(false);
  else {
    state.error = "";
    sendFrame({ type: "notifications-req" });
    if (location.pathname === "/notifications") loadPage(true);
  }
  render();
}

function enter() {
  closePopover(false);
  if (state.connected) loadPage(true);
  const id = new URL(location.href).searchParams.get("id");
  if (id && state.loaded.some((item) => item.id === id)) select(id);
  else if (!id && state.loaded.length) chooseFirstVisible();
}

export function initNotifications(send) {
  sendFrame = send;
  document.querySelector("#notification-bell").addEventListener("click", togglePopover);
  document.querySelector("#notifications-read-all").addEventListener("click", markAll);
  document.querySelector("#notification-popover-read-all").addEventListener("click", markAll);
  document.querySelector("#notification-load-older").addEventListener("click", () => loadPage(false));
  document.querySelector("#notification-detail-curtain").addEventListener("click", closeMobileDetail);
  addEventListener("themepopoveropen", () => closePopover(false));
  document.addEventListener("pointerdown", (event) => {
    const popover = document.querySelector("#notification-popover");
    const bell = document.querySelector("#notification-bell");
    if (!popover.hidden && !popover.contains(event.target) && !bell.contains(event.target)) closePopover();
  });
  document.addEventListener("keydown", (event) => {
    const popover = document.querySelector("#notification-popover");
    if (event.key === "Escape") {
      if (!popover.hidden) closePopover();
      else closeMobileDetail();
    } else if (event.key === "Tab" && !popover.hidden) {
      const focusable = [...popover.querySelectorAll("a[href],button:not([disabled])")];
      if (event.shiftKey && document.activeElement === focusable[0]) {
        event.preventDefault(); focusable.at(-1)?.focus();
      } else if (!event.shiftKey && document.activeElement === focusable.at(-1)) {
        event.preventDefault(); focusable[0]?.focus();
      }
    }
  });
  addEventListener("popstate", () => {
    if (location.pathname !== "/notifications") return;
    const id = new URL(location.href).searchParams.get("id");
    if (id) select(id);
    else closeMobileDetail();
  });
  render();
  return { frame, status, enter, closePopover };
}
