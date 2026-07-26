// @ts-nocheck
const ORDER = {
  "": ["title", "url", "head", "body"],
  head: ["lang", "description", "canonical", "og"],
  node: ["tag", "title", "url", "text", "href", "src", "alt", "type", "name", "value",
    "placeholder", "action", "method", "label", "rows", "items", "children", "note"],
};

function pruned(value, inSequence = false) {
  if (Array.isArray(value)) {
    return value.map((item) => pruned(item, true)).filter((item) => item !== undefined &&
      (!(Array.isArray(item)) || item.length) &&
      (!(item && typeof item === "object" && !Array.isArray(item)) || Object.keys(item).length));
  }
  if (value && typeof value === "object") {
    const result = {};
    for (const [key, item] of Object.entries(value)) {
      const clean = pruned(item, false);
      if (clean === undefined || clean === null || clean === "") continue;
      if (Array.isArray(clean) && clean.length === 0) continue;
      if (clean && typeof clean === "object" && !Array.isArray(clean) &&
          Object.keys(clean).length === 0) continue;
      result[key] = clean;
    }
    return result;
  }
  if (value === null || value === undefined) return undefined;
  if (value === "" && !inSequence) return undefined;
  return value;
}

function keys(value, context) {
  const priority = ORDER[context] ?? [];
  const set = new Set(priority);
  return [...priority.filter((key) => Object.hasOwn(value, key)),
    ...Object.keys(value).filter((key) => !set.has(key)).sort()];
}

function scalar(value) {
  if (typeof value === "string") return JSON.stringify(value);
  if (value === null) return "null";
  return String(value);
}

function encode(value, indent, context) {
  if (Array.isArray(value)) {
    if (!value.length) return "[]";
    const pad = "\t".repeat(indent);
    const lines = [];
    for (const item of value) {
      if (Array.isArray(item)) {
        lines.push(`${pad}-`);
        lines.push(encode(item, indent + 1, context));
      } else if (item && typeof item === "object") {
        const inner = encode(item, indent + 1, "node").split("\n");
        lines.push(`${pad}- ${inner[0].replace("\t".repeat(indent + 1), "")}`);
        lines.push(...inner.slice(1));
      } else {
        lines.push(`${pad}- ${scalar(item)}`);
      }
    }
    return lines.join("\n");
  }
  if (value && typeof value === "object") {
    const pad = "\t".repeat(indent);
    const lines = [];
    for (const key of keys(value, context)) {
      const item = value[key];
      let next = context;
      if (context === "" && (key === "head" || key === "body")) next = key;
      else if (context === "head" && key === "og") next = "head";
      else if (context === "body" || context === "head") next = "node";
      if (Array.isArray(item) || (item && typeof item === "object")) {
        lines.push(`${pad}${key}:`);
        lines.push(encode(item, indent + 1, next));
      } else {
        lines.push(`${pad}${key}: ${scalar(item)}`);
      }
    }
    return lines.join("\n");
  }
  return scalar(value);
}

export function digestYAML(digest) {
  return encode(pruned(JSON.parse(JSON.stringify(digest))), 0, "") + "\n";
}
