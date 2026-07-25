import { openLightbox } from "./tools.js";
import { renderTree } from "./tree.js";
import { parseYamlLite } from "./yaml-lite.js";

/**
 * @typedef {{name: string, kind: "dir"|"file", size: number, mtimeMs: number}} DataEntry
 */

export const DATA_ROOT_IGNORED = new Set(["chromium", "mail", "metrics", "valkey", "xdg"]);
const VIEW_KEY = "vm-data-view";
const SORT_KEY = "vm-data-sort";
const SPLIT_KEY = "vm-data-split";
const DEFAULT_SPLIT = 66;

/** @param {number} bytes */
function formatSize(bytes) {
  if (!Number.isFinite(bytes) || bytes < 0) return "—";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  const digits = unit === 0 ? 0 : value >= 10 ? 1 : 2;
  return `${value.toFixed(digits)} ${units[unit]}`;
}

/** @param {string} name */
function extensionOf(name) {
  const index = name.lastIndexOf(".");
  return index < 0 ? "" : name.slice(index).toLowerCase();
}

/** @param {string} value */
export function sanitizeDataPath(value) {
  if (typeof value !== "string" || value.includes("\0") || value.startsWith("/") || value.startsWith("\\")) {
    return null;
  }
  const normalized = value.replaceAll("\\", "/");
  const segments = normalized.split("/").filter((segment) => segment !== "" && segment !== ".");
  if (segments.some((segment) => segment === "..")) return null;
  return segments.join("/");
}

/**
 * @param {DataEntry[]} entries
 * @param {"name"|"size"|"mtime"} field
 * @param {"asc"|"desc"} direction
 * @param {Map<string, number>} sizes
 */
export function sortDataEntries(entries, field, direction, sizes = new Map()) {
  const sign = direction === "desc" ? -1 : 1;
  return entries.toSorted((left, right) => {
    if (left.kind !== right.kind) return left.kind === "dir" ? -1 : 1;
    let result = 0;
    if (field === "name") {
      result = left.name.localeCompare(right.name);
    } else if (field === "size") {
      const leftSize = left.kind === "dir" ? (sizes.get(left.name) ?? -1) : left.size;
      const rightSize = right.kind === "dir" ? (sizes.get(right.name) ?? -1) : right.size;
      result = leftSize - rightSize;
    } else {
      result = left.mtimeMs - right.mtimeMs;
    }
    return result === 0 ? left.name.localeCompare(right.name) : result * sign;
  });
}

/** @param {string} name */
function iconFor(name, kind) {
  if (kind === "dir") return "folder";
  const ext = extensionOf(name);
  if ([".png", ".jpg", ".jpeg", ".webp", ".gif"].includes(ext)) return "image";
  if (ext === ".wav") return "volume-2";
  if ([".json", ".jsonl", ".yaml", ".yml"].includes(ext)) return "file-braces";
  return "file";
}

/** @param {string} name @param {string} className */
function icon(name, className = "icon") {
  const namespace = "http:" + "//www.w3.org/2000/svg";
  const svg = document.createElementNS(namespace, "svg");
  svg.setAttribute("class", className === "icon" ? className : `icon ${className}`);
  svg.setAttribute("aria-hidden", "true");
  const use = document.createElementNS(namespace, "use");
  use.setAttribute("href", `/icons.svg#i-${name}`);
  svg.append(use);
  return svg;
}

/** Interactive read-only explorer of $VM_DATA_DIR. */
export function initData() {
  const page = /** @type {HTMLElement} */ (document.querySelector(".data-page"));
  const grid = /** @type {HTMLElement} */ (document.querySelector(".data-grid"));
  const entriesHost = /** @type {HTMLElement} */ (document.querySelector("#data-tree"));
  const breadcrumbs = /** @type {HTMLElement} */ (document.querySelector("#data-breadcrumbs"));
  const viewer = /** @type {HTMLElement} */ (document.querySelector("#data-viewer"));
  const splitter = /** @type {HTMLElement} */ (document.querySelector("#data-splitter"));
  const sortButtons = [...document.querySelectorAll("[data-data-sort]")];
  const viewButtons = [...document.querySelectorAll("[data-data-view]")];

  let active = false;
  let cwd = "";
  let entries = /** @type {DataEntry[]} */ ([]);
  let directorySizes = new Map();
  let selected = "";
  let navigationID = 0;
  let previewID = 0;
  let lastSyncedURL = "";
  let view = localStorage.getItem(VIEW_KEY) === "icons" ? "icons" : "list";
  const savedSort = localStorage.getItem(SORT_KEY)?.split(":") ?? [];
  let sortField = ["name", "size", "mtime"].includes(savedSort[0]) ? savedSort[0] : "name";
  let sortDirection = savedSort[1] === "desc" ? "desc" : "asc";
  const savedSplit = Number.parseFloat(localStorage.getItem(SPLIT_KEY) ?? "");
  setSplit(Number.isFinite(savedSplit) ? savedSplit : DEFAULT_SPLIT, false);

  /** @param {string} path */
  async function list(path) {
    const response = await fetch(`/api/data/list?path=${encodeURIComponent(path)}`);
    if (!response.ok) {
      const error = new Error(`Unable to open path (${response.status})`);
      Object.assign(error, {status: response.status});
      throw error;
    }
    const body = await response.json();
    return Array.isArray(body.entries) ? /** @type {DataEntry[]} */ (body.entries) : [];
  }

  /** @param {string} path */
  function dataURL(path) {
    const url = new URL("/data", location.origin);
    if (path) url.searchParams.set("path", path);
    return `${url.pathname}${url.search}`;
  }

  /** @param {string} path @param {"push"|"replace"|"none"} mode */
  function setURL(path, mode) {
    if (mode === "none") return;
    const url = dataURL(path);
    if (`${location.pathname}${location.search}` === url) return;
    history[mode === "replace" ? "replaceState" : "pushState"](null, "", url);
    lastSyncedURL = location.href;
  }

  function updateControls() {
    for (const button of sortButtons) {
      const pressed = button.getAttribute("data-data-sort") === sortField;
      button.setAttribute("aria-pressed", String(pressed));
      button.replaceChildren(document.createTextNode(button.getAttribute("data-data-sort") === "mtime" ? "Modified" :
        `${button.getAttribute("data-data-sort")?.[0].toUpperCase()}${button.getAttribute("data-data-sort")?.slice(1)}`));
      if (pressed) button.append(icon(sortDirection === "asc" ? "arrow-up" : "arrow-down"));
    }
    for (const button of viewButtons) {
      button.setAttribute("aria-pressed", String(button.getAttribute("data-data-view") === view));
    }
  }

  function renderBreadcrumbs() {
    breadcrumbs.replaceChildren();
    const parts = cwd ? cwd.split("/") : [];
    const root = document.createElement("button");
    root.type = "button";
    root.textContent = "Data";
    root.title = "Data volume root";
    root.addEventListener("click", () => void openDirectory("", "push"));
    breadcrumbs.append(root);
    let path = "";
    for (const part of parts) {
      breadcrumbs.append(icon("chevron-right"));
      path = path ? `${path}/${part}` : part;
      const target = path;
      const button = document.createElement("button");
      button.type = "button";
      button.textContent = part;
      button.title = target;
      button.addEventListener("click", () => void openDirectory(target, "push"));
      breadcrumbs.append(button);
    }
  }

  /** @param {DataEntry} entry */
  function effectiveSize(entry) {
    return entry.kind === "dir" ? directorySizes.get(entry.name) : entry.size;
  }

  /** @param {"name"|"size"|"mtime"} field */
  function changeSort(field) {
    if (sortField === field) {
      sortDirection = sortDirection === "asc" ? "desc" : "asc";
    } else {
      sortField = field;
      sortDirection = "asc";
    }
    localStorage.setItem(SORT_KEY, `${sortField}:${sortDirection}`);
    renderEntries();
  }

  function renderListHeader() {
    const header = document.createElement("div");
    header.className = "data-list-head";
    const spacer = document.createElement("span");
    spacer.setAttribute("aria-hidden", "true");
    header.append(spacer);
    for (const [field, label] of [["name", "Name"], ["size", "Size"], ["mtime", "Modified"]]) {
      const button = document.createElement("button");
      button.type = "button";
      button.textContent = label;
      button.setAttribute("aria-pressed", String(sortField === field));
      if (sortField === field) button.append(icon(sortDirection === "asc" ? "arrow-up" : "arrow-down"));
      button.addEventListener("click", () => changeSort(/** @type {"name"|"size"|"mtime"} */ (field)));
      header.append(button);
    }
    entriesHost.append(header);
  }

  /** @param {DataEntry} entry @param {number} largest */
  function renderListEntry(entry, largest) {
    const path = cwd ? `${cwd}/${entry.name}` : entry.name;
    const row = document.createElement("button");
    row.type = "button";
    row.className = "data-list-row";
    row.title = path;
    if (selected === path) row.setAttribute("aria-current", "true");
    row.append(icon(iconFor(entry.name, entry.kind), "data-entry-icon"));

    const name = document.createElement("span");
    name.className = "data-entry-name";
    name.textContent = entry.name;
    name.title = entry.name;
    row.append(name);

    const sizeCell = document.createElement("span");
    sizeCell.className = "data-size-cell";
    const bar = document.createElement("span");
    bar.className = "data-size-bar";
    const size = effectiveSize(entry);
    bar.style.setProperty("--data-size", `${size === undefined || largest === 0 ? 0 : Math.max(2, size / largest * 100)}%`);
    const sizeText = document.createElement("span");
    sizeText.textContent = size === undefined ? "Calculating…" : formatSize(size);
    sizeCell.append(bar, sizeText);
    row.append(sizeCell);

    const modified = document.createElement("time");
    modified.dateTime = new Date(entry.mtimeMs).toISOString();
    modified.textContent = new Date(entry.mtimeMs).toLocaleString();
    row.append(modified);
    row.addEventListener("click", () => void activateEntry(entry));
    entriesHost.append(row);
  }

  /** @param {DataEntry} entry */
  function renderIconEntry(entry) {
    const path = cwd ? `${cwd}/${entry.name}` : entry.name;
    const tile = document.createElement("button");
    tile.type = "button";
    tile.className = "data-icon-tile";
    tile.title = path;
    if (selected === path) tile.setAttribute("aria-current", "true");
    tile.append(icon(iconFor(entry.name, entry.kind), "data-tile-icon"));
    const name = document.createElement("span");
    name.className = "data-entry-name";
    name.textContent = entry.name;
    name.title = entry.name;
    const size = document.createElement("span");
    size.className = "data-tile-size";
    const bytes = effectiveSize(entry);
    size.textContent = bytes === undefined ? "Calculating…" : formatSize(bytes);
    tile.append(name, size);
    tile.addEventListener("click", () => void activateEntry(entry));
    entriesHost.append(tile);
  }

  function renderEntries() {
    updateControls();
    renderBreadcrumbs();
    entriesHost.replaceChildren();
    entriesHost.className = `data-entries ${view === "list" ? "data-list" : "data-icons"}`;
    let visible = cwd === "" ? entries.filter((entry) => !DATA_ROOT_IGNORED.has(entry.name)) : entries;
    visible = sortDataEntries(visible, /** @type {"name"|"size"|"mtime"} */ (sortField),
      /** @type {"asc"|"desc"} */ (sortDirection), directorySizes);
    if (visible.length === 0) {
      const empty = document.createElement("p");
      empty.className = "data-empty";
      empty.textContent = "This directory is empty.";
      entriesHost.append(empty);
      return;
    }
    if (view === "list") {
      renderListHeader();
      const largest = Math.max(0, ...visible.map((entry) => effectiveSize(entry) ?? 0));
      for (const entry of visible) renderListEntry(entry, largest);
    } else {
      for (const entry of visible) renderIconEntry(entry);
    }
  }

  /** @param {DataEntry} entry */
  async function activateEntry(entry) {
    const path = cwd ? `${cwd}/${entry.name}` : entry.name;
    if (entry.kind === "dir") {
      await openDirectory(path, "push");
      return;
    }
    selected = path;
    setURL(path, "push");
    renderEntries();
    await showFile(path, entry);
  }

  /** @param {string} path @param {"push"|"replace"|"none"} urlMode */
  async function openDirectory(path, urlMode) {
    const id = ++navigationID;
    entriesHost.setAttribute("aria-busy", "true");
    try {
      const nextEntries = await list(path);
      if (id !== navigationID) return;
      cwd = path;
      entries = nextEntries;
      directorySizes = new Map();
      selected = "";
      setURL(path, urlMode);
      closePreview(false);
      renderEntries();
      void loadDirectorySizes(path, id);
    } catch (error) {
      if (id !== navigationID) return;
      showExplorerError(error);
    } finally {
      if (id === navigationID) entriesHost.removeAttribute("aria-busy");
    }
  }

  /** @param {string} path @param {number} id */
  async function loadDirectorySizes(path, id) {
    try {
      const response = await fetch(`/api/data/du?path=${encodeURIComponent(path)}`);
      if (!response.ok) return;
      const body = await response.json();
      if (id !== navigationID || path !== cwd || typeof body.sizes !== "object" || body.sizes === null) return;
      directorySizes = new Map(Object.entries(body.sizes)
        .filter(([, size]) => Number.isFinite(size))
        .map(([name, size]) => [name, Number(size)]));
      renderEntries();
    } catch {
      // Recursive sizes are optional; the listing remains usable.
    }
  }

  /** @param {unknown} error */
  function showExplorerError(error) {
    entriesHost.replaceChildren();
    const note = document.createElement("p");
    note.className = "data-empty data-error";
    note.textContent = error instanceof Error ? error.message : String(error);
    entriesHost.append(note);
  }

  /** @param {unknown} error */
  function showViewerError(error) {
    viewer.replaceChildren();
    const note = document.createElement("p");
    note.className = "data-empty data-error";
    note.textContent = error instanceof Error ? error.message : String(error);
    viewer.append(note);
  }

  function renderViewerPlaceholder() {
    viewer.replaceChildren();
    const placeholder = document.createElement("p");
    placeholder.className = "data-empty";
    placeholder.textContent = "Select a file to inspect it.";
    viewer.append(placeholder);
  }

  /** @param {boolean} updateURL */
  function closePreview(updateURL) {
    previewID += 1;
    selected = "";
    viewer.classList.remove("open");
    document.body.classList.remove("data-preview-locked");
    renderViewerPlaceholder();
    if (updateURL) {
      setURL(cwd, "push");
      renderEntries();
    }
  }

  /** @param {string} path @param {DataEntry} entry */
  async function showFile(path, entry) {
    const id = ++previewID;
    viewer.replaceChildren();
    viewer.classList.add("open");
    if (matchMedia("(max-width: 47.999rem)").matches) document.body.classList.add("data-preview-locked");
    const header = document.createElement("header");
    header.className = "data-viewer-head";
    const back = document.createElement("button");
    back.type = "button";
    back.className = "icon-button data-viewer-back";
    back.setAttribute("aria-label", "Back to files");
    back.append(icon("chevron-left"));
    back.addEventListener("click", () => closePreview(true));
    const title = document.createElement("div");
    const pathEl = document.createElement("strong");
    pathEl.textContent = path;
    pathEl.title = path;
    const meta = document.createElement("p");
    meta.className = "data-meta";
    meta.textContent = `${formatSize(entry.size)} · ${new Date(entry.mtimeMs).toLocaleString()}`;
    title.append(pathEl, meta);
    const download = document.createElement("a");
    download.href = `/api/data/file?path=${encodeURIComponent(path)}&download=1`;
    download.textContent = "Download";
    download.className = "button data-download";
    download.prepend(icon("download"));
    header.append(back, title, download);
    viewer.append(header);

    const ext = extensionOf(entry.name);
    const fileURL = `/api/data/file?path=${encodeURIComponent(path)}`;
    try {
      if ([".png", ".jpg", ".jpeg", ".webp", ".gif"].includes(ext)) {
        const img = document.createElement("img");
        img.src = fileURL;
        img.alt = entry.name;
        img.className = "data-preview";
        img.addEventListener("click", () => openLightbox(fileURL, entry.name));
        viewer.append(img);
        return;
      }
      if (ext === ".wav") {
        const audio = document.createElement("audio");
        audio.controls = true;
        audio.src = fileURL;
        viewer.append(audio);
        return;
      }
      const response = await fetch(fileURL);
      if (!response.ok) throw new Error(`Unable to preview file (${response.status})`);
      const truncated = response.headers.get("X-VM-Truncated") === "1";
      const text = await response.text();
      if (id !== previewID || selected !== path) return;
      if (ext === ".json") {
        try {
          viewer.append(renderTree(JSON.parse(text), {expandDepth: 2}));
          if (truncated) appendTruncationNotice(viewer, path);
          return;
        } catch {
          // Fall through to text.
        }
      }
      if (ext === ".yaml" || ext === ".yml") {
        try {
          viewer.append(renderTree(parseYamlLite(text)));
          if (truncated) appendTruncationNotice(viewer, path);
          return;
        } catch {
          // Fall through to text.
        }
      }
      if (ext === ".jsonl") {
        const rows = document.createElement("div");
        rows.className = "data-jsonl";
        for (const [index, line] of text.split("\n").entries()) {
          if (!line.trim()) continue;
          try {
            const branch = renderTree(JSON.parse(line), {expandDepth: 0});
            const row = document.createElement("div");
            row.className = "data-jsonl-row";
            const heading = document.createElement("strong");
            heading.textContent = `line ${index + 1}`;
            row.append(heading, branch);
            rows.append(row);
          } catch {
            const pre = document.createElement("pre");
            pre.textContent = line;
            rows.append(pre);
          }
        }
        viewer.append(rows);
        if (truncated) appendTruncationNotice(viewer, path);
        return;
      }
      if (response.headers.get("Content-Type")?.startsWith("text/") ||
          response.headers.get("Content-Type")?.includes("json") ||
          [".txt", ".log", ".md", ".sh", ".go", ".js", ".css", ".html", ".pem"].includes(ext)) {
        const pre = document.createElement("pre");
        pre.textContent = text;
        viewer.append(pre);
        if (truncated) appendTruncationNotice(viewer, path);
        return;
      }
      const card = document.createElement("p");
      card.className = "data-meta";
      card.textContent = `Binary file · ${response.headers.get("Content-Type") || "unknown type"} · ${formatSize(entry.size)}`;
      viewer.append(card);
    } catch (error) {
      if (id !== previewID || selected !== path) return;
      const note = document.createElement("p");
      note.className = "data-empty data-error";
      note.textContent = error instanceof Error ? error.message : String(error);
      viewer.append(note);
    }
  }

  /** @param {HTMLElement} host @param {string} path */
  function appendTruncationNotice(host, path) {
    const note = document.createElement("p");
    note.className = "data-meta";
    const link = document.createElement("a");
    link.href = `/api/data/file?path=${encodeURIComponent(path)}&download=1`;
    link.textContent = "Download full file";
    note.append("Preview truncated. ", link);
    host.append(note);
  }

  async function syncFromURL() {
    const syncURL = location.href;
    if (syncURL === lastSyncedURL) return;
    lastSyncedURL = syncURL;
    const raw = new URL(location.href).searchParams.get("path") ?? "";
    const target = sanitizeDataPath(raw);
    if (target === null) {
      await openDirectory("", "replace");
      showViewerError(new Error("Invalid data path."));
      return;
    }
    if (target === "") {
      await openDirectory("", "none");
      return;
    }
    try {
      await openDirectory(target, "none");
      if (cwd === target) return;
    } catch {
      // openDirectory renders its own error; file detection follows below.
    }
    const slash = target.lastIndexOf("/");
    const parent = slash < 0 ? "" : target.slice(0, slash);
    const name = slash < 0 ? target : target.slice(slash + 1);
    try {
      const parentEntries = await list(parent);
      const entry = parentEntries.find((item) => item.name === name);
      if (!entry) throw new Error("Data path not found.");
      if (entry.kind === "dir") {
        await openDirectory(target, "none");
        return;
      }
      await openDirectory(parent, "none");
      selected = target;
      renderEntries();
      await showFile(target, entry);
    } catch (error) {
      await openDirectory(parent, "none");
      showViewerError(error);
    }
  }

  /** @param {number} value @param {boolean} persist */
  function setSplit(value, persist = true) {
    const split = Math.min(80, Math.max(20, value));
    grid.style.setProperty("--data-split", `${split}%`);
    splitter.setAttribute("aria-valuenow", String(Math.round(split)));
    if (persist) localStorage.setItem(SPLIT_KEY, split.toFixed(2));
  }

  for (const button of sortButtons) {
    button.addEventListener("click", () => changeSort(
      /** @type {"name"|"size"|"mtime"} */ (button.getAttribute("data-data-sort")),
    ));
  }
  for (const button of viewButtons) {
    button.addEventListener("click", () => {
      view = /** @type {"icons"|"list"} */ (button.getAttribute("data-data-view"));
      localStorage.setItem(VIEW_KEY, view);
      renderEntries();
    });
  }

  splitter.addEventListener("pointerdown", (event) => {
    if (event.button !== 0) return;
    splitter.setPointerCapture(event.pointerId);
    document.body.classList.add("data-resizing");
  });
  splitter.addEventListener("pointermove", (event) => {
    if (!splitter.hasPointerCapture(event.pointerId)) return;
    const bounds = grid.getBoundingClientRect();
    setSplit((event.clientX - bounds.left) / bounds.width * 100);
  });
  splitter.addEventListener("pointerup", (event) => {
    if (splitter.hasPointerCapture(event.pointerId)) splitter.releasePointerCapture(event.pointerId);
    document.body.classList.remove("data-resizing");
  });
  splitter.addEventListener("dblclick", () => setSplit(DEFAULT_SPLIT));
  splitter.addEventListener("keydown", (event) => {
    const current = Number.parseFloat(grid.style.getPropertyValue("--data-split")) || DEFAULT_SPLIT;
    if (event.key === "ArrowLeft") setSplit(current - 2);
    else if (event.key === "ArrowRight") setSplit(current + 2);
    else if (event.key === "Home") setSplit(20);
    else if (event.key === "End") setSplit(80);
    else return;
    event.preventDefault();
  });
  addEventListener("keydown", (event) => {
    if (event.key === "Escape" && viewer.classList.contains("open") &&
        matchMedia("(max-width: 47.999rem)").matches) closePreview(true);
  });
  addEventListener("popstate", () => {
    if (active && location.pathname === "/data") void syncFromURL();
  });

  updateControls();
  return {
    /** @param {string} shownPage */
    show(shownPage) {
      active = shownPage === "data";
      if (!active) {
        document.body.classList.remove("data-preview-locked");
        return;
      }
      if (location.href !== lastSyncedURL) void syncFromURL();
      page.scrollTop = 0;
    },
  };
}
