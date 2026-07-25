(() => {
  const TEXT_CAP = 300;
  const HREF_CAP = 300;
  const ATTR_CAP = 120;
  const CELL_CAP = 80;
  const MAX_ROWS = 40;
  const MAX_ITEMS = 40;
  const NODE_BUDGET = 800;
  const PRUNE = new Set(["script", "style", "noscript", "template"]);
  const SEMANTIC = new Set([
    "h1", "h2", "h3", "h4", "h5", "h6", "a", "img", "video", "audio", "iframe",
    "table", "form", "input", "select", "textarea", "button", "label", "ul", "ol",
    "dl", "li", "p", "blockquote", "pre", "code", "time", "figcaption",
  ]);
  let kept = 0;
  let budgetHit = false;

  function normalize(text, cap) {
    let value = String(text ?? "").replace(/\s+/g, " ").trim();
    if (!value) return "";
    if (value.length > cap) return value.slice(0, cap) + "…";
    return value;
  }

  function escapeID(id) {
    return String(id).replace(/([ !"#$%&'()*+,./:;<=>?@[\\\]^`{|}~])/g, "\\$1");
  }

  function nthOfType(el) {
    const tag = el.tagName.toLowerCase();
    let count = 0;
    for (const child of el.parentElement.children) {
      if (child.tagName.toLowerCase() === tag) count++;
      if (child === el) return count;
    }
    return 1;
  }

  function selectorFor(el) {
    const parts = [];
    let current = el;
    while (current && current !== document.body) {
      const id = current.getAttribute("id");
      if (id) {
        parts.unshift("#" + escapeID(id));
        break;
      }
      parts.unshift(current.tagName.toLowerCase() + ":nth-of-type(" + nthOfType(current) + ")");
      current = current.parentElement;
    }
    if (current === document.body || (current && current.parentElement === document.body)) {
      return "body > " + parts.join(" > ");
    }
    return parts.join(" > ");
  }

  function isVisible(el) {
    if (!el || el.nodeType !== 1) return false;
    const rects = el.getClientRects();
    if (!rects || rects.length === 0) return false;
    const style = window.getComputedStyle(el);
    return style.visibility !== "hidden";
  }

  function ownText(el) {
    let text = "";
    for (const node of el.childNodes) {
      if (node.nodeType === 3) text += node.nodeValue;
    }
    return normalize(text, TEXT_CAP);
  }

  function fullText(el) {
    let text = "";
    const walk = (node) => {
      if (node.nodeType === 3) text += node.nodeValue;
      else if (node.nodeType === 1 && isVisible(node)) {
        for (const child of node.childNodes) walk(child);
      }
    };
    for (const child of el.childNodes) walk(child);
    return normalize(text, TEXT_CAP);
  }

  function resolveURL(raw) {
    if (!raw) return "";
    try {
      return new URL(raw, location.href).href;
    } catch {
      return String(raw).slice(0, HREF_CAP);
    }
  }

  function capURL(raw) {
    const url = resolveURL(raw);
    return url.length > HREF_CAP ? url.slice(0, HREF_CAP) + "…" : url;
  }

  function capAttr(raw) {
    const value = String(raw ?? "");
    return value.length > ATTR_CAP ? value.slice(0, ATTR_CAP) + "…" : value;
  }

  function labelFor(el) {
    const id = el.getAttribute("id");
    if (id) {
      for (const label of document.getElementsByTagName("label")) {
        if (label.getAttribute("for") === id) {
          const text = normalize(label.textContent, ATTR_CAP);
          if (text) return text;
        }
      }
    }
    let parent = el.parentElement;
    while (parent) {
      if (parent.tagName.toLowerCase() === "label") {
        const text = normalize(parent.textContent, ATTR_CAP);
        if (text) return text;
      }
      parent = parent.parentElement;
    }
    return "";
  }

  function hasOwnContent(node) {
    if (!node || typeof node !== "object") return false;
    return Boolean(node.text || node.href || node.src || node.alt || node.type ||
      node.name || node.value || node.placeholder || node.action || node.rows ||
      node.items || node.note);
  }

  function collapse(nodes) {
    const out = [];
    for (const node of nodes) {
      if (Array.isArray(node.children)) node.children = collapse(node.children);
      if (Array.isArray(node.items)) {
        node.items = node.items.map((item) => {
          if (item && Array.isArray(item.children)) item.children = collapse(item.children);
          return item;
        });
      }
      // Content-free non-semantic wrappers are hoisted away even if they have children.
      if (!hasOwnContent(node) && !SEMANTIC.has(node.tag)) {
        if (Array.isArray(node.children)) out.push(...node.children);
        continue;
      }
      out.push(node);
    }
    return out;
  }

  function extractHead() {
    const head = {};
    const lang = document.documentElement.getAttribute("lang");
    if (lang) head.lang = capAttr(lang);
    for (const meta of document.head.getElementsByTagName("meta")) {
      const name = meta.getAttribute("name");
      if (name === "description") {
        const content = capAttr(meta.getAttribute("content"));
        if (content) head.description = content;
      }
      const prop = meta.getAttribute("property");
      if (prop && prop.startsWith("og:")) {
        if (!head.og) head.og = {};
        const key = prop.slice(3);
        const content = capAttr(meta.getAttribute("content"));
        if (content) head.og[key] = content;
      }
    }
    if (head.og) {
      const keys = Object.keys(head.og).sort();
      const sorted = {};
      for (const key of keys) sorted[key] = head.og[key];
      head.og = sorted;
    }
    for (const link of document.head.getElementsByTagName("link")) {
      if (link.getAttribute("rel") === "canonical") {
        const href = capURL(link.getAttribute("href"));
        if (href) head.canonical = href;
      }
    }
    return head;
  }

  function extractTable(table) {
    const rows = [];
    const trs = table.getElementsByTagName("tr");
    let truncated = false;
    for (let i = 0; i < trs.length && rows.length < MAX_ROWS; i++) {
      const cells = [];
      for (const cell of trs[i].children) {
        const tag = cell.tagName.toLowerCase();
        if (tag === "td" || tag === "th") cells.push(normalize(fullText(cell), CELL_CAP));
      }
      if (cells.length) rows.push(cells);
    }
    if (trs.length > MAX_ROWS) truncated = true;
    if (truncated) rows.push(["…truncated"]);
    return rows;
  }

  function extractListItem(li) {
    const links = [];
    const walkLinks = (el) => {
      if (el.nodeType === 1) {
        const tag = el.tagName.toLowerCase();
        if (tag === "a" && el.getAttribute("href")) links.push(el);
        for (const child of el.children) walkLinks(child);
      }
    };
    walkLinks(li);
    let structural = false;
    const walkStruct = (el) => {
      if (el === li || el.nodeType !== 1) return;
      const tag = el.tagName.toLowerCase();
      if (SEMANTIC.has(tag) && tag !== "a") structural = true;
      for (const child of el.children) walkStruct(child);
    };
    walkStruct(li);
    if (!structural) {
      const item = { text: fullText(li) };
      if (links.length === 1) item.href = capURL(links[0].getAttribute("href"));
      return item;
    }
    return null;
  }

  function extractList(list) {
    const tag = list.tagName.toLowerCase();
    const items = [];
    let truncated = false;
    const children = list.children;
    for (let i = 0; i < children.length && items.length < MAX_ITEMS; i++) {
      const child = children[i];
      const childTag = child.tagName.toLowerCase();
      if (tag === "dl") {
        if (childTag === "dt" || childTag === "dd") {
          items.push({ tag: childTag, sel: selectorFor(child), text: fullText(child) });
        }
      } else if (childTag === "li") {
        const flat = extractListItem(child);
        if (flat) items.push(flat);
        else {
          const node = walkElement(child);
          if (node) items.push(node);
        }
      }
    }
    if (children.length > MAX_ITEMS) truncated = true;
    if (truncated) items.push({ note: "…truncated" });
    return items;
  }

  function populateNode(el, node) {
    const tag = el.tagName.toLowerCase();
    if (tag === "a" || tag === "area") {
      const href = capURL(el.getAttribute("href"));
      if (href) node.href = href;
    }
    if (tag === "img" || tag === "video" || tag === "audio" || tag === "source" ||
        tag === "iframe" || tag === "embed") {
      const src = capURL(el.getAttribute("src"));
      if (src) node.src = src;
    }
    if (tag === "img") {
      const alt = capAttr(el.getAttribute("alt"));
      if (alt) node.alt = alt;
    }
    if (tag === "form") {
      const action = capURL(el.getAttribute("action") || location.href);
      if (action) node.action = action;
      const method = (el.getAttribute("method") || "get").toLowerCase();
      node.method = method;
    }
    if (tag === "input" || tag === "select" || tag === "textarea" || tag === "button") {
      const type = capAttr(el.getAttribute("type") || (tag === "textarea" ? "textarea" : tag));
      if (type) node.type = type;
      const name = capAttr(el.getAttribute("name"));
      if (name) node.name = name;
      const placeholder = capAttr(el.getAttribute("placeholder"));
      if (placeholder) node.placeholder = placeholder;
      if (type !== "password") {
        const value = capAttr(el.getAttribute("value") || (tag === "textarea" ? el.textContent : ""));
        if (value) node.value = value;
      }
      const label = labelFor(el);
      if (label) node.label = label;
    }
    const text = ownText(el);
    if (text) node.text = text;
  }

  function walkElement(el) {
    if (budgetHit) return null;
    const tag = el.tagName.toLowerCase();
    if (PRUNE.has(tag)) return null;
    if (el.getAttribute("aria-hidden") === "true") return null;
    if (tag === "svg") {
      const label = capAttr(el.getAttribute("aria-label"));
      if (!label) return null;
      kept++;
      return { tag: "svg", sel: selectorFor(el), text: label };
    }
    if (!isVisible(el)) return null;

    if (tag === "table") {
      kept++;
      const node = { tag, sel: selectorFor(el), rows: extractTable(el) };
      return node;
    }
    if (tag === "ul" || tag === "ol" || tag === "dl") {
      kept++;
      const node = { tag, sel: selectorFor(el), items: extractList(el) };
      return node;
    }

    kept++;
    if (kept >= NODE_BUDGET) {
      budgetHit = true;
      return { note: "truncated: node budget reached" };
    }

    const node = { tag, sel: selectorFor(el) };
    populateNode(el, node);

    const childTags = new Set(["input", "select", "textarea", "button", "img", "a"]);
    const skipChildren = tag === "table" || tag === "ul" || tag === "ol" || tag === "dl";
    if (!skipChildren) {
      const children = [];
      for (const child of el.children) {
        if (budgetHit) break;
        const walked = walkElement(child);
        if (walked) children.push(walked);
      }
      if (children.length) node.children = children;
    } else if (tag === "form") {
      const children = [];
      for (const child of el.children) {
        if (budgetHit) break;
        const childTag = child.tagName.toLowerCase();
        if (childTags.has(childTag) || childTag === "label") {
          const walked = walkElement(child);
          if (walked) children.push(walked);
        }
      }
      if (children.length) node.children = children;
    }
    return node;
  }

  const body = [];
  if (document.body) {
    for (const child of document.body.children) {
      if (budgetHit) break;
      const walked = walkElement(child);
      if (walked) body.push(walked);
    }
  }
  const collapsed = collapse(body);
  if (budgetHit && collapsed.length &&
      collapsed[collapsed.length - 1].note !== "truncated: node budget reached") {
    collapsed.push({ note: "truncated: node budget reached" });
  }
  return {
    title: document.title || "",
    url: location.href,
    head: extractHead(),
    body: collapsed,
  };
})()
