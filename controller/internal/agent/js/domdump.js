(() => {
  const OMIT = new Set(["script", "style", "noscript", "template"]);
  const SECRET_QUERY = /^(?:auth|token|key|signature|sig)$/i;

  function directText(el) {
    let value = "";
    for (const child of el.childNodes) {
      if (child.nodeType === 3) value += child.nodeValue || "";
    }
    return value;
  }

  function visible(el) {
    const style = window.getComputedStyle(el);
    if (style.display === "none" || style.visibility === "hidden") return false;
    const rects = el.getClientRects();
    return Boolean(rects && rects.length) ||
      style.display === "contents" ||
      Boolean(el.children && el.children.length);
  }

  function safeAttribute(el, name, value) {
    if (name === "value" && el.getAttribute("type")?.toLowerCase() === "password") {
      return "";
    }
    if (!["href", "src", "action"].includes(name)) return value;
    try {
      const parsed = new URL(value, location.href);
      for (const key of [...parsed.searchParams.keys()]) {
        if (SECRET_QUERY.test(key)) parsed.searchParams.delete(key);
      }
      return parsed.href;
    } catch {
      return value;
    }
  }

  function serialize(el) {
    const tag = el.tagName.toLowerCase();
    if (OMIT.has(tag)) return null;
    const attrs = {};
    for (const attr of el.attributes || []) {
      const value = safeAttribute(el, attr.name, attr.value);
      if (value) attrs[attr.name] = value;
    }
    const node = { tag, visible: visible(el) };
    if (Object.keys(attrs).length) node.attrs = attrs;
    const text = directText(el);
    if (text) node.text = text;
    const children = [];
    for (const child of el.children || []) {
      const serialized = serialize(child);
      if (serialized) children.push(serialized);
    }
    if (children.length) node.children = children;
    return node;
  }

  function serializeChildren(root) {
    const nodes = [];
    for (const child of root?.children || []) {
      const serialized = serialize(child);
      if (serialized) nodes.push(serialized);
    }
    return nodes;
  }

  return {
    url: location.href,
    title: document.title || "",
    lang: document.documentElement.getAttribute("lang") || "",
    head: serializeChildren(document.head),
    body: serializeChildren(document.body),
  };
})()
