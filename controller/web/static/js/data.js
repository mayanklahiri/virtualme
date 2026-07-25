import { openLightbox } from "./tools.js";
import { renderTree } from "./tree.js";
import { parseYamlLite } from "./yaml-lite.js";

/**
 * @typedef {{name: string, kind: "dir"|"file", size: number, mtimeMs: number}} DataEntry
 */

/** @param {number} bytes */
function formatSize(bytes) {
  if (!Number.isFinite(bytes) || bytes < 0) return "";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
}

/** @param {string} name */
function extensionOf(name) {
  const index = name.lastIndexOf(".");
  return index < 0 ? "" : name.slice(index).toLowerCase();
}

/**
 * Interactive read-only explorer of $VM_DATA_DIR.
 * @param {(page: string) => void} [onReady]
 */
export function initData() {
  const tree = /** @type {HTMLElement} */ (document.querySelector("#data-tree"));
  const viewer = /** @type {HTMLElement} */ (document.querySelector("#data-viewer"));
  /** @type {Map<string, DataEntry[]>} */
  const cache = new Map();
  /** @type {string | null} */
  let selected = null;
  let loaded = false;

  async function list(path) {
    if (cache.has(path)) return /** @type {DataEntry[]} */ (cache.get(path));
    const response = await fetch(`/api/data/list?path=${encodeURIComponent(path)}`);
    if (!response.ok) throw new Error(`list failed (${response.status})`);
    const body = await response.json();
    const entries = Array.isArray(body.entries) ? body.entries : [];
    cache.set(path, entries);
    return entries;
  }

  /** @param {string} path @param {DataEntry[]} entries @param {HTMLElement} host */
  function renderEntries(path, entries, host) {
    host.replaceChildren();
    if (entries.length === 0) {
      const empty = document.createElement("p");
      empty.className = "data-empty";
      empty.textContent = "(empty)";
      host.append(empty);
      return;
    }
    for (const entry of entries) {
      const childPath = path ? `${path}/${entry.name}` : entry.name;
      if (entry.kind === "dir") {
        const branch = document.createElement("div");
        branch.className = "tree-branch";
        const toggle = document.createElement("button");
        toggle.type = "button";
        toggle.className = "tree-toggle";
        toggle.setAttribute("aria-expanded", "false");
        toggle.textContent = `${entry.name}/`;
        const body = document.createElement("div");
        body.className = "tree-body";
        body.hidden = true;
        toggle.addEventListener("click", async () => {
          const open = toggle.getAttribute("aria-expanded") === "true";
          if (open) {
            toggle.setAttribute("aria-expanded", "false");
            body.hidden = true;
            return;
          }
          toggle.setAttribute("aria-expanded", "true");
          body.hidden = false;
          if (!body.dataset.loaded) {
            try {
              const children = await list(childPath);
              renderEntries(childPath, children, body);
              body.dataset.loaded = "1";
            } catch (error) {
              body.textContent = String(error);
            }
          }
        });
        branch.append(toggle, body);
        host.append(branch);
      } else {
        const row = document.createElement("button");
        row.type = "button";
        row.className = "data-file";
        row.dataset.path = childPath;
        if (selected === childPath) row.setAttribute("aria-current", "true");
        const name = document.createElement("span");
        name.className = "tree-key";
        name.textContent = entry.name;
        const size = document.createElement("span");
        size.className = "tree-val";
        size.textContent = formatSize(entry.size);
        row.append(name, size);
        row.addEventListener("click", () => {
          selected = childPath;
          for (const button of tree.querySelectorAll(".data-file[aria-current]")) {
            button.removeAttribute("aria-current");
          }
          row.setAttribute("aria-current", "true");
          void showFile(childPath, entry);
        });
        host.append(row);
      }
    }
  }

  /** @param {string} path @param {DataEntry} entry */
  async function showFile(path, entry) {
    viewer.replaceChildren();
    const header = document.createElement("header");
    header.className = "data-viewer-head";
    const title = document.createElement("div");
    const pathEl = document.createElement("strong");
    pathEl.textContent = path;
    const meta = document.createElement("p");
    meta.className = "data-meta";
    meta.textContent = `${formatSize(entry.size)} · ${new Date(entry.mtimeMs).toLocaleString()}`;
    title.append(pathEl, meta);
    const download = document.createElement("a");
    download.href = `/api/data/file?path=${encodeURIComponent(path)}&download=1`;
    download.textContent = "Download";
    download.className = "data-download";
    header.append(title, download);
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
      if (!response.ok) throw new Error(`file failed (${response.status})`);
      const truncated = response.headers.get("X-VM-Truncated") === "1";
      const text = await response.text();
      if (ext === ".json") {
        try {
          viewer.append(renderTree(JSON.parse(text), { expandDepth: 2 }));
          if (truncated) appendTruncationNotice(viewer, path);
          return;
        } catch {
          // fall through to text
        }
      }
      if (ext === ".yaml" || ext === ".yml") {
        try {
          viewer.append(renderTree(parseYamlLite(text)));
          if (truncated) appendTruncationNotice(viewer, path);
          return;
        } catch {
          // fall through to text
        }
      }
      if (ext === ".jsonl") {
        const list = document.createElement("div");
        list.className = "data-jsonl";
        for (const [index, line] of text.split("\n").entries()) {
          if (!line.trim()) continue;
          try {
            const branch = renderTree(JSON.parse(line), { expandDepth: 0 });
            const label = document.createElement("div");
            label.className = "data-jsonl-row";
            const heading = document.createElement("strong");
            heading.textContent = `line ${index + 1}`;
            label.append(heading, branch);
            list.append(label);
          } catch {
            const pre = document.createElement("pre");
            pre.textContent = line;
            list.append(pre);
          }
        }
        viewer.append(list);
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
      const note = document.createElement("p");
      note.className = "data-empty";
      note.textContent = String(error);
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

  async function refresh() {
    cache.clear();
    selected = null;
    tree.replaceChildren();
    viewer.replaceChildren();
    const placeholder = document.createElement("p");
    placeholder.className = "data-empty";
    placeholder.textContent = "Select a file to inspect it.";
    viewer.append(placeholder);
    try {
      const entries = await list("");
      renderEntries("", entries, tree);
      loaded = true;
    } catch (error) {
      tree.textContent = String(error);
    }
  }

  return {
    /** @param {string} page */
    show(page) {
      if (page === "data") {
        void refresh();
      } else if (loaded) {
        cache.clear();
      }
    },
  };
}
