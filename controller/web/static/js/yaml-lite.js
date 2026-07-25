// Hand-written parser for the deterministic YAML subset emitted by read_page.

/**
 * @param {string} text
 * @returns {any}
 */
export function parseYamlLite(text) {
  const lines = String(text ?? "").replace(/\r\n/g, "\n").split("\n");
  /** @type {{line: string, indent: number}[]} */
  const nodes = [];
  for (const raw of lines) {
    if (!raw.trim() || raw.trim().startsWith("#")) continue;
    const indent = raw.match(/^\t*/)?.[0].length ?? 0;
    const line = raw.slice(indent);
    if (line.startsWith(" ")) throw new Error("space indentation is not allowed");
    if (line.includes("\t")) throw new Error("tabs are only allowed as indentation");
    nodes.push({ indent, line });
  }
  const parsed = parseMapping(nodes, 0, -1);
  if (!parsed.value || typeof parsed.value !== "object" || Array.isArray(parsed.value)) {
    throw new Error("expected mapping at root");
  }
  return parsed.value;
}

/**
 * @param {{line: string, indent: number}[]} nodes
 * @param {number} index
 * @param {number} indent
 */
function parseMapping(nodes, index, indent) {
  /** @type {Record<string, any>} */
  const mapping = {};
  let cursor = index;
  while (cursor < nodes.length && nodes[cursor].indent > indent) {
    const node = nodes[cursor];
    if (node.line === "-" || node.line.startsWith("- ")) break;
    const colon = node.line.indexOf(":");
    if (colon < 0) throw new Error("expected key");
    const key = node.line.slice(0, colon);
    const rest = node.line.slice(colon + 1).trim();
    cursor++;
    if (rest === "") {
      if (cursor < nodes.length && nodes[cursor].indent > node.indent &&
          (nodes[cursor].line.startsWith("- ") || nodes[cursor].line === "-")) {
        const seq = parseSequence(nodes, cursor, node.indent);
        mapping[key] = seq.value;
        cursor = seq.index;
      } else {
        const child = parseMapping(nodes, cursor, node.indent);
        mapping[key] = child.value;
        cursor = child.index;
      }
      continue;
    }
    mapping[key] = parseScalar(rest);
  }
  return { value: mapping, index: cursor };
}

/**
 * @param {{line: string, indent: number}[]} nodes
 * @param {number} index
 * @param {number} indent
 */
function parseSequence(nodes, index, indent) {
  /** @type {any[]} */
  const items = [];
  let cursor = index;
  while (cursor < nodes.length && nodes[cursor].indent > indent &&
         (nodes[cursor].line.startsWith("- ") || nodes[cursor].line === "-")) {
    const node = nodes[cursor];
    if (node.line === "-") {
      // Bare dash: a nested sequence (e.g. table rows) at deeper indent.
      cursor++;
      if (cursor < nodes.length && nodes[cursor].indent > node.indent) {
        if (nodes[cursor].line.startsWith("- ") || nodes[cursor].line === "-") {
          const child = parseSequence(nodes, cursor, node.indent);
          items.push(child.value);
          cursor = child.index;
        } else {
          const child = parseMapping(nodes, cursor, node.indent);
          items.push(child.value);
          cursor = child.index;
        }
      } else {
        throw new Error("bare sequence dash without nested block");
      }
      continue;
    }
    const itemText = node.line.slice(2);
    cursor++;
    if (itemText.startsWith('"') || /^-?\d+$/.test(itemText)) {
      items.push(parseScalar(itemText));
      continue;
    }
    if (itemText.includes(": ") && !itemText.endsWith(":")) {
      const itemColon = itemText.indexOf(": ");
      const itemKey = itemText.slice(0, itemColon);
      const itemRest = itemText.slice(itemColon + 2);
      /** @type {Record<string, any>} */
      const item = { [itemKey]: parseScalar(itemRest) };
      // Subsequent mapping keys sit one tab deeper; parseMapping wants
      // children with indent > parentIndent, so pass the dash-line indent.
      const child = parseMapping(nodes, cursor, node.indent);
      Object.assign(item, child.value);
      cursor = child.index;
      items.push(item);
      continue;
    }
    if (itemText.endsWith(":")) {
      const itemKey = itemText.slice(0, -1);
      const child = parseMapping(nodes, cursor, node.indent);
      items.push({ [itemKey]: child.value });
      cursor = child.index;
      continue;
    }
    items.push(parseScalar(itemText));
  }
  return { value: items, index: cursor };
}

/** @param {string} raw */
function parseScalar(raw) {
  const text = raw.trim();
  if (/^-?\d+$/.test(text)) return Number(text);
  if (!text.startsWith('"')) throw new Error("only double-quoted strings are supported");
  return JSON.parse(text);
}
