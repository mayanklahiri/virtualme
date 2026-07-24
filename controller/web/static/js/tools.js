/** @typedef {Record<string, any>} Data */

/** @param {string} description */
function firstSentence(description) {
  const match = String(description ?? "").match(/^.*?[.!?](?:\s|$)/);
  return (match?.[0] ?? description ?? "").trim();
}

/** @param {string} name */
function fieldID(name) {
  return `tool-field-${name.replaceAll(/[^a-z0-9_-]/gi, "-")}`;
}

/** Full-screen image overlay with a download control. @param {string} src @param {string} alt */
function openLightbox(src, alt) {
  const previous = /** @type {HTMLElement | null} */ (document.activeElement);
  const overlay = document.createElement("div");
  overlay.className = "lightbox";
  overlay.setAttribute("role", "dialog");
  overlay.setAttribute("aria-label", alt);
  const image = document.createElement("img");
  image.src = src;
  image.alt = alt;
  const actions = document.createElement("div");
  actions.className = "lightbox-actions";
  const download = document.createElement("a");
  download.textContent = "Download";
  download.href = src;
  download.download = `${alt.toLowerCase().replaceAll(/[^a-z0-9]+/g, "-").replaceAll(/^-|-$/g, "") || "tool-result"}.png`;
  const close = document.createElement("button");
  close.type = "button";
  close.textContent = "Close";
  actions.append(download, close);
  overlay.append(image, actions);
  function dismiss() {
    overlay.remove();
    document.removeEventListener("keydown", onKey);
    previous?.focus();
  }
  /** @param {KeyboardEvent} event */
  function onKey(event) {
    if (event.key === "Escape") {
      event.preventDefault();
      dismiss();
    }
  }
  overlay.addEventListener("click", (event) => {
    if (event.target !== download) dismiss();
  });
  close.addEventListener("click", dismiss);
  document.addEventListener("keydown", onKey);
  document.body.append(overlay);
  close.focus();
}

/** @param {(value: Data) => void} send */
export function initTools(send) {
  const list = /** @type {HTMLElement} */ (document.querySelector("#tools-list"));
  const form = /** @type {HTMLFormElement} */ (document.querySelector("#tool-form"));
  const output = /** @type {HTMLElement} */ (document.querySelector("#tool-output"));
  /** @type {Data[]} */
  let tools = [];
  /** @type {Data | undefined} */
  let selected;
  /** @type {Map<string, Data>} */
  const results = new Map();
  /** @type {Map<string, {tool: string, timer: ReturnType<typeof setTimeout>}>} */
  const pending = new Map();

  function renderOutput() {
    const result = selected ? results.get(String(selected.name)) : undefined;
    if (!result) {
      output.hidden = true;
      output.replaceChildren();
      return;
    }
    output.hidden = false;
    const header = document.createElement("header");
    const title = document.createElement("strong");
    title.textContent = String(selected?.name ?? "");
    const status = document.createElement("span");
    status.className = `tool-result-status ${result.ok ? "ok" : "error"}`;
    status.textContent = `${result.ok ? "ok" : "error"} · ${Number(result.durationMs) || 0} ms`;
    header.append(title, status);
    const body = document.createElement("div");
    body.className = "tool-output-body";
    if (result.image) {
      const alt = `${selected?.name ?? "Tool"} result`;
      const zoom = document.createElement("button");
      zoom.type = "button";
      zoom.className = "tool-image-zoom";
      zoom.setAttribute("aria-label", `Open ${alt} full screen`);
      const image = document.createElement("img");
      image.src = String(result.image);
      image.alt = alt;
      zoom.append(image);
      zoom.addEventListener("click", () => openLightbox(String(result.image), alt));
      body.append(zoom);
    }
    const text = String(result.text || result.error || "");
    if (text) {
      const pre = document.createElement("pre");
      try {
        pre.textContent = JSON.stringify(JSON.parse(text), null, 2);
      } catch {
        pre.textContent = text;
      }
      body.append(pre);
    }
    if (result.error && result.text) {
      const error = document.createElement("p");
      error.className = "tool-field-error";
      error.textContent = String(result.error);
      body.append(error);
    }
    output.replaceChildren(header, body);
  }

  /** @param {Data} tool */
  function selectTool(tool) {
    selected = tool;
    for (const button of list.querySelectorAll("button")) {
      button.setAttribute("aria-pressed", button.dataset.tool === tool.name ? "true" : "false");
    }
    buildForm();
    renderOutput();
  }

  function renderList() {
    const fragment = document.createDocumentFragment();
    for (const tool of tools) {
      const button = document.createElement("button");
      button.type = "button";
      button.dataset.tool = String(tool.name);
      button.setAttribute("aria-pressed", tool.name === selected?.name ? "true" : "false");
      const name = document.createElement("strong");
      name.textContent = String(tool.name);
      const description = document.createElement("span");
      description.textContent = firstSentence(String(tool.description ?? ""));
      button.append(name, description);
      button.addEventListener("click", () => selectTool(tool));
      fragment.append(button);
    }
    list.replaceChildren(fragment);
    if (!selected && tools.length > 0) selectTool(tools[0]);
  }

  function buildForm() {
    if (!selected) {
      form.hidden = true;
      return;
    }
    form.hidden = false;
    const schema = selected.schema ?? {};
    const properties = schema.properties ?? {};
    const required = new Set(Array.isArray(schema.required) ? schema.required : []);
    const fragment = document.createDocumentFragment();
    const heading = document.createElement("h2");
    heading.textContent = String(selected.name);
    const description = document.createElement("p");
    description.className = "tool-form-description";
    description.textContent = String(selected.description ?? "");
    fragment.append(heading, description);
    for (const [name, propertyValue] of Object.entries(properties)) {
      const property = /** @type {Data} */ (propertyValue);
      const group = document.createElement("div");
      group.className = "tool-field";
      const id = fieldID(name);
      const label = document.createElement("label");
      label.htmlFor = id;
      label.textContent = `${name}${required.has(name) ? " *" : ""}`;
      /** @type {HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement} */
      let control;
      if (property.type === "string" && Array.isArray(property.enum)) {
        const select = document.createElement("select");
        if (!required.has(name)) select.append(new Option("…", ""));
        for (const value of property.enum) select.append(new Option(String(value), String(value)));
        control = select;
      } else if (property.type === "string" &&
        (Number(property.maxLength) > 200 || ["command", "expression", "text"].includes(name))) {
        control = document.createElement("textarea");
        control.rows = 4;
      } else if (property.type === "integer" || property.type === "number") {
        const input = document.createElement("input");
        input.type = "number";
        if (property.minimum !== undefined) input.min = String(property.minimum);
        if (property.maximum !== undefined) input.max = String(property.maximum);
        if (property.type === "integer") input.step = "1";
        control = input;
      } else if (property.type === "boolean") {
        const input = document.createElement("input");
        input.type = "checkbox";
        control = input;
      } else if (property.type === "array" || property.type === "object") {
        const textarea = document.createElement("textarea");
        textarea.rows = 4;
        textarea.dataset.json = "true";
        control = textarea;
      } else {
        const input = document.createElement("input");
        input.type = "text";
        control = input;
      }
      control.id = id;
      control.name = name;
      if (required.has(name)) control.required = true;
      if (property.maxLength !== undefined && "maxLength" in control) {
        control.maxLength = Number(property.maxLength);
      }
      if (property.default !== undefined && !(control instanceof HTMLInputElement && control.type === "checkbox")) {
        control.value = typeof property.default === "object"
          ? JSON.stringify(property.default, null, 2)
          : String(property.default);
      }
      const help = document.createElement("small");
      help.textContent = String(property.description ?? "");
      const error = document.createElement("small");
      error.className = "tool-field-error";
      error.dataset.errorFor = name;
      group.append(label, control, help, error);
      fragment.append(group);
    }
    const actions = document.createElement("div");
    actions.className = "tool-form-actions";
    const invoke = document.createElement("button");
    invoke.type = "submit";
    invoke.textContent = pendingTool(selected.name) ? "queued…" : "Invoke";
    invoke.disabled = pendingTool(selected.name);
    const queueNote = document.createElement("small");
    queueNote.textContent = "Runs through the job queue; a busy agent finishes first.";
    actions.append(invoke, queueNote);
    const trust = document.createElement("p");
    trust.className = "tool-trust-note";
    trust.textContent = "Trusted-network console; no additional auth (see spec 002 trust model).";
    fragment.append(actions, trust);
    form.replaceChildren(fragment);
  }

  /** @param {unknown} tool */
  function pendingTool(tool) {
    for (const value of pending.values()) {
      if (value.tool === tool) return true;
    }
    return false;
  }

  form.addEventListener("submit", (event) => {
    event.preventDefault();
    if (!selected || pendingTool(selected.name)) return;
    /** @type {Data} */
    const args = {};
    let valid = true;
    const required = new Set(Array.isArray(selected.schema?.required) ? selected.schema.required : []);
    for (const [name, propertyValue] of Object.entries(selected.schema?.properties ?? {})) {
      const property = /** @type {Data} */ (propertyValue);
      const control = /** @type {HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement} */ (
        form.elements.namedItem(name)
      );
      const error = /** @type {HTMLElement} */ (form.querySelector(`[data-error-for="${CSS.escape(name)}"]`));
      error.textContent = "";
      if (property.type === "boolean" && control instanceof HTMLInputElement) {
        args[name] = control.checked;
        continue;
      }
      const value = control.value.trim();
      if (!value) {
        if (required.has(name)) {
          error.textContent = "Required.";
          valid = false;
        }
        continue;
      }
      if (control.dataset.json === "true") {
        try {
          args[name] = JSON.parse(value);
        } catch {
          error.textContent = "Enter valid JSON.";
          valid = false;
        }
      } else if (property.type === "integer") {
        args[name] = Number.parseInt(value, 10);
      } else if (property.type === "number") {
        args[name] = Number(value);
      } else {
        args[name] = value;
      }
    }
    if (!valid || !form.reportValidity()) return;
    const id = crypto.randomUUID();
    const tool = String(selected.name);
    const timer = setTimeout(() => {
      pending.delete(id);
      results.set(tool, {
        type: "tool-result", id, ok: false, durationMs: 120000,
        text: "", image: "", error: "Tool invocation timed out after 120 seconds.",
      });
      if (selected?.name === tool) {
        buildForm();
        renderOutput();
      }
    }, 120000);
    pending.set(id, { tool, timer });
    buildForm();
    send({ type: "tool-invoke", id, tool, args });
  });

  return {
    enter() {
      if (tools.length === 0) send({ type: "tools-list-req" });
    },
    /** @param {Data} message */
    frame(message) {
      if (message.type === "tools-list") {
        tools = Array.isArray(message.tools) ? message.tools : [];
        if (selected) selected = tools.find((tool) => tool.name === selected?.name);
        renderList();
        return;
      }
      if (message.type !== "tool-result") return;
      const waiting = pending.get(String(message.id));
      if (!waiting) return;
      clearTimeout(waiting.timer);
      pending.delete(String(message.id));
      results.set(waiting.tool, message);
      if (selected?.name === waiting.tool) {
        buildForm();
        renderOutput();
      }
    },
  };
}
