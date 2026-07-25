/**
 * Render a parsed JSON/YAML value as a collapsible tree.
 * @param {any} value
 * @param {{expandDepth?: number}} [options]
 * @returns {HTMLElement}
 */
export function renderTree(value, options = {}) {
  const expandDepth = options.expandDepth ?? 2;
  const root = document.createElement("div");
  root.className = "tree";
  root.append(renderNode(value, "", 0, expandDepth));
  return root;
}

/**
 * @param {any} value
 * @param {string} label
 * @param {number} depth
 * @param {number} expandDepth
 */
function renderNode(value, label, depth, expandDepth) {
  if (Array.isArray(value)) {
    return renderBranch(label || "items", value, depth, expandDepth, true);
  }
  if (value && typeof value === "object") {
    return renderBranch(label || "object", value, depth, expandDepth, false);
  }
  return renderLeaf(label, formatScalar(value));
}

/**
 * @param {string} label
 * @param {any} value
 * @param {number} depth
 * @param {number} expandDepth
 * @param {boolean} isArray
 */
function renderBranch(label, value, depth, expandDepth, isArray) {
  const branch = document.createElement("div");
  branch.className = "tree-branch";
  const count = isArray ? value.length : Object.keys(value).length;
  const title = label ? `${label} (${count})` : `(${count})`;
  const open = depth < expandDepth;
  const toggle = document.createElement("button");
  toggle.type = "button";
  toggle.className = "tree-toggle";
  toggle.setAttribute("aria-expanded", open ? "true" : "false");
  toggle.textContent = title;
  const body = document.createElement("div");
  body.className = "tree-body";
  body.hidden = !open;
  toggle.addEventListener("click", () => {
    const expanded = toggle.getAttribute("aria-expanded") === "true";
    toggle.setAttribute("aria-expanded", expanded ? "false" : "true");
    body.hidden = expanded;
  });
  branch.append(toggle, body);
  if (isArray) {
    for (const [index, item] of value.entries()) {
      body.append(renderEntry(String(index), item, depth + 1, expandDepth));
    }
  } else {
    for (const key of Object.keys(value).sort()) {
      body.append(renderEntry(key, value[key], depth + 1, expandDepth));
    }
  }
  return branch;
}

/**
 * @param {string} key
 * @param {any} value
 * @param {number} depth
 * @param {number} expandDepth
 */
function renderEntry(key, value, depth, expandDepth) {
  const row = document.createElement("div");
  row.className = "tree-row";
  if (value && typeof value === "object") {
    row.append(renderNode(value, key, depth, expandDepth));
    return row;
  }
  row.append(renderLeaf(key, formatScalar(value)));
  return row;
}

/** @param {string} key @param {string} value */
function renderLeaf(key, value) {
  const row = document.createElement("div");
  row.className = "tree-row tree-leaf";
  if (key) {
    const keyEl = document.createElement("span");
    keyEl.className = "tree-key";
    keyEl.textContent = key;
    row.append(keyEl);
  }
  const valEl = document.createElement("span");
  valEl.className = "tree-val";
  valEl.textContent = value;
  row.append(valEl);
  return row;
}

/** @param {any} value */
function formatScalar(value) {
  if (typeof value === "string") return JSON.stringify(value);
  if (value === null || value === undefined) return "null";
  return String(value);
}
