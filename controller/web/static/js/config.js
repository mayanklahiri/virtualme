// @ts-nocheck
import {
  buildSettingTree,
  cloneConfig,
  conflictMessage,
  getConfigPath,
  humanize,
  issueControl,
  orderedSections,
  parseEditorValue,
  restartComplete,
  restartMessage,
  secretStatusLabel,
  setConfigPath,
  validateSecretReference,
  validateStringItem,
} from "./config-model.js";

const sectionRenderers = new Set([
  "vm-config-network-section", "vm-config-service-section", "vm-config-inference-section",
  "vm-config-agent-section", "vm-config-mail-section",
  "vm-config-health-section", "vm-config-integrations-section",
]);

function element(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function typeInfo(setting) {
  if (setting.secret) return { icon: "key-round", label: "secret" };
  if (setting.choices?.length) return { icon: "chevrons-up-down", label: "enum" };
  if (setting.type === "boolean") return { icon: "toggle-left", label: "boolean" };
  if (setting.type === "integer" || setting.type === "number") return { icon: "hash", label: "number" };
  if (setting.type === "array") return { icon: "list", label: "list" };
  return { icon: "type", label: "text" };
}

function typeIcon(setting) {
  const metadata = typeInfo(setting);
  const namespace = "http:" + "//www.w3.org/2000/svg";
  const svg = document.createElementNS(namespace, "svg");
  svg.className = "icon config-type-icon";
  svg.setAttribute("aria-label", metadata.label);
  svg.setAttribute("title", metadata.label);
  const use = document.createElementNS(namespace, "use");
  use.setAttribute("href", `/icons.svg#i-${metadata.icon}`);
  svg.append(use);
  return svg;
}

function constraints(setting, input) {
  const rules = setting.constraints ?? {};
  if (rules.minimum !== undefined) input.min = String(rules.minimum);
  if (rules.maximum !== undefined) input.max = String(rules.maximum);
  if (rules.minLength !== undefined) input.minLength = rules.minLength;
  if (rules.maxLength !== undefined) input.maxLength = rules.maxLength;
  if (rules.pattern) input.pattern = rules.pattern;
  if (rules.pattern || rules.minLength > 0) input.required = true;
}

export function initConfig() {
  const root = document.querySelector("#config-content");
  const sectionNavigation = document.querySelector("#config-sections");
  const editButton = document.querySelector("#config-edit");
  const saveButton = document.querySelector("#config-save");
  const discardButton = document.querySelector("#config-discard");
  const restartButton = document.querySelector("#config-restart");
  const status = document.querySelector("#config-status");
  let schema;
  let snapshot;
  let draft;
  let editing = false;
  let loaded = false;
  let selectedSectionId = "";
  let expectedRestartHash = "";
  let restartTimer;
  const controls = new Map();

  function availableSections() {
    return orderedSections(schema?.sections ?? []).filter((section) =>
      section.id !== "integrations" || (section.settings ?? []).length);
  }

  function sectionForAnchor(anchor) {
    return availableSections().find((section) =>
      section.anchor === anchor || (section.settings ?? []).some((setting) => setting.anchor === anchor));
  }

  function selectSection(id) {
    if (!availableSections().some((section) => section.id === id)) return;
    selectedSectionId = id;
    globalThis.localStorage?.setItem("vm-config-section", id);
  }

  function initializeSectionSelection() {
    const sections = availableSections();
    const hashSection = sectionForAnchor(globalThis.location?.hash?.slice(1) ?? "");
    const remembered = globalThis.localStorage?.getItem("vm-config-section") ?? "";
    selectedSectionId = hashSection?.id ??
      (sections.some((section) => section.id === remembered) ? remembered : sections[0]?.id ?? "");
    if (selectedSectionId) globalThis.localStorage?.setItem("vm-config-section", selectedSectionId);
  }

  async function request(url, options) {
    const response = await fetch(url, options);
    const body = await response.json();
    if (!response.ok) throw Object.assign(new Error(body.error?.message ?? response.statusText), { response, body });
    return body;
  }

  async function load() {
    status.textContent = "Loading configuration…";
    try {
      [schema, snapshot] = await Promise.all([
        request("/api/config/schema"),
        request("/api/config"),
      ]);
      draft = cloneConfig(snapshot.raw);
      initializeSectionSelection();
      loaded = true;
      status.textContent = "";
      render();
    } catch (error) {
      status.textContent = `Configuration unavailable: ${error.message}`;
    }
  }

  function settingCard(setting) {
    const card = element("article", "config-setting");
    card.id = setting.anchor;
    const heading = element("div", "config-setting-head");
    heading.append(element("h3", "", humanize(setting.path.split(".").at(-1))));
    if (setting.restart) heading.append(element("span", "config-restart-badge", setting.restart));
    heading.append(typeIcon(setting));
    card.append(
      heading,
      element("p", "config-path", setting.path),
      element("p", "config-overview", setting.overview ?? ""),
    );
    return card;
  }

  function readSetting(setting) {
    const card = settingCard(setting);
    let display = "";
    if (setting.secret) {
      const unresolved = getConfigPath(snapshot.raw, setting.path);
      display = unresolved ? String(unresolved) : "Not configured";
    } else {
      const effective = getConfigPath(snapshot.effective, setting.path);
      const raw = getConfigPath(snapshot.raw, setting.path);
      display = String(effective ?? raw ?? setting.default ?? "");
    }
    card.append(element("p", "config-value", display));
    const effective = getConfigPath(snapshot.effective, setting.path);
    if (!setting.secret && setting.default !== undefined &&
      JSON.stringify(effective) !== JSON.stringify(setting.default)) {
      card.append(element("p", "config-default", `Default: ${String(setting.default)}`));
    }
    const secret = setting.secret ? snapshot.secrets?.[setting.path] : null;
    if (secret?.configured && secret.status !== "inactive") {
      const refresh = element("button", "config-secret-refresh", "Refresh secret");
      refresh.type = "button";
      refresh.addEventListener("click", async () => {
        refresh.disabled = true;
        status.textContent = `Refreshing ${setting.path}…`;
        try {
          await request("/api/config/secrets/refresh", {
            method: "POST", headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ path: setting.path }),
          });
          await load();
        } catch (error) {
          status.textContent = error.message;
          refresh.disabled = false;
        }
      });
      card.append(element("p", "config-overview", secretStatusLabel(secret)));
      card.append(refresh);
    }
    return card;
  }

  const componentRenderers = {
    "vm-text-field": (setting, value) => makeInput(setting, value, "text"),
    "vm-number-field": (setting, value) => makeInput(setting, value, "number"),
    "vm-checkbox": (setting, value) => {
      const input = makeInput(setting, value, "checkbox");
      input.checked = Boolean(value);
      return input;
    },
    "vm-select": (setting, value) => {
      const select = document.createElement("select");
      for (const choice of setting.choices ?? []) {
        const option = element("option", "", String(choice.value ?? choice));
        option.value = String(choice.value ?? choice);
        option.selected = option.value === String(value);
        select.append(option);
      }
      return select;
    },
    "vm-secret-reference": (setting, value) => makeInput(setting, value, "text"),
    "vm-path-field": (setting, value) => makeInput(setting, value, "text"),
    "vm-address-field": (setting, value) => makeInput(setting, value, "text"),
    "vm-readonly-field": (setting, value) => {
      const input = makeInput(setting, value, "text");
      input.readOnly = true;
      return input;
    },
    "vm-string-list": (setting, value) => makeInput(setting, JSON.stringify(value), "text"),
  };

  function makeInput(setting, value, type) {
    const input = document.createElement("input");
    input.type = type;
    if (type !== "checkbox") input.value = String(value ?? "");
    constraints(setting, input);
    return input;
  }

  function editSetting(setting) {
    const row = settingCard(setting);
    row.classList.add("config-editor");
    if (setting.ui?.component === "vm-string-list") {
      const editor = element("div", "config-list-editor");
      const rows = element("div", "config-list-rows");
      const listError = element("p", "config-list-error");
      const values = [...(getConfigPath(draft, setting.path) ?? [])];
      const sync = () => {
        try {
          setConfigPath(draft, setting.path, parseEditorValue(values, setting));
          listError.textContent = "";
        } catch (error) {
          listError.textContent = error.message;
        }
      };
      const draw = () => {
        rows.replaceChildren();
        controls.delete(setting.path);
        let lastInput;
        values.forEach((value, index) => {
          const line = element("div", "config-list-row");
          const input = makeInput({ constraints: setting.item?.constraints }, value, "text");
          input.required = false;
          input.dataset.configPath = setting.path;
          input.addEventListener("input", () => {
            values[index] = input.value;
            try {
              if (input.value.trim() !== "") {
                validateStringItem(input.value.trim(), setting.item);
              }
              input.setCustomValidity("");
            } catch (error) {
              input.setCustomValidity(error.message);
            }
            sync();
          });
          const remove = element("button", "button secondary config-list-remove", "Remove");
          remove.type = "button";
          remove.addEventListener("click", () => {
            values.splice(index, 1);
            draw();
            sync();
          });
          line.append(input, remove);
          rows.append(line);
          lastInput = input;
          if (!controls.has(setting.path)) controls.set(setting.path, input);
        });
        return lastInput;
      };
      const add = element("button", "button secondary config-list-add", "Add");
      add.type = "button";
      add.addEventListener("click", () => {
        values.push("");
        draw()?.focus();
      });
      editor.append(rows, listError, add);
      draw();
      row.append(editor);
      return row;
    }
    const renderer = componentRenderers[setting.ui?.component];
    if (!renderer) {
      row.append(element("strong", "config-error", `Unsupported component ${setting.ui?.component}`));
      return row;
    }
    const input = renderer(setting, getConfigPath(draft, setting.path));
    input.dataset.configPath = setting.path;
    const update = () => {
      try {
        const raw = input.type === "checkbox" ? input.checked : input.value;
        if (setting.secret && !validateSecretReference(String(raw))) {
          throw new Error("Use an empty value, ${env:NAME}, ${file:/absolute/path}, or ${file:${data}/relative/path}.");
        }
        setConfigPath(draft, setting.path, parseEditorValue(raw, setting));
        input.setCustomValidity("");
      } catch (error) {
        input.setCustomValidity(error.message);
      }
    };
    input.addEventListener("change", update);
    input.addEventListener("input", update);
    controls.set(setting.path, input);
    const control = element("label", "config-control");
    input.setAttribute("aria-label", humanize(setting.path.split(".").at(-1)));
    control.append(input);
    row.append(control);
    return row;
  }

  function renderTreeNode(node, editing) {
    const container = element("div", "config-group");
    if (node.depth) {
      container.dataset.depth = String(node.depth);
      const title = element("h3", "config-group-title");
      const parent = node.path.split(".").slice(0, -1).join(".");
      title.append(element("strong", "", humanize(node.name)));
      if (parent) title.append(element("span", "", `${parent}.`));
      container.append(title);
    }
    for (const child of [...node.children.values()].sort((left, right) => left.name.localeCompare(right.name))) {
      container.append(renderTreeNode(child, editing));
    }
    for (const setting of node.settings) {
      container.append(editing ? editSetting(setting) : readSetting(setting));
    }
    return container;
  }

  function renderSection(section) {
    const renderer = section.ui?.sectionRenderer;
    if (!sectionRenderers.has(renderer)) {
      return element("p", "config-error", `Unsupported section renderer ${renderer}`);
    }
    const container = element("section", `config-section ${renderer}`);
    container.id = section.anchor;
    container.append(element("h2", "", section.title));
    if (section.overview) container.append(element("p", "config-section-overview", section.overview));
    container.append(renderTreeNode(buildSettingTree(section.settings ?? []), editing));
    return container;
  }

  function render() {
    root.replaceChildren();
    sectionNavigation.replaceChildren();
    controls.clear();
    const sections = availableSections();
    if (!sections.some((section) => section.id === selectedSectionId)) {
      selectSection(sections[0]?.id ?? "");
    }
    for (const section of sections) {
      const button = element("button");
      button.type = "button";
      button.setAttribute("aria-pressed", String(section.id === selectedSectionId));
      button.append(
        element("strong", "", section.title),
        element("span", "", String(section.overview ?? "").split(/(?<=[.!?])\s/, 1)[0]),
      );
      button.addEventListener("click", () => {
        selectSection(section.id);
        render();
      });
      sectionNavigation.append(button);
    }
    const selected = sections.find((section) => section.id === selectedSectionId);
    if (selected) root.append(renderSection(selected));
    editButton.hidden = editing;
    saveButton.hidden = !editing;
    discardButton.hidden = !editing;
    restartButton.hidden = !snapshot.pendingRestart;
    if (snapshot.pendingRestart) {
      restartButton.textContent = `Restart to update (${snapshot.restartServices.join(", ")})`;
    }
    const anchor = globalThis.location?.hash?.slice(1);
    if (anchor && sectionForAnchor(anchor)?.id === selectedSectionId) {
      document.getElementById?.(anchor)?.scrollIntoView();
    }
  }

  editButton.addEventListener("click", () => {
    editing = true;
    draft = cloneConfig(snapshot.raw);
    render();
  });
  discardButton.addEventListener("click", () => {
    editing = false;
    draft = cloneConfig(snapshot.raw);
    render();
  });
  saveButton.addEventListener("click", async () => {
    const invalid = root.querySelector("[data-config-path]:invalid");
    if (invalid) {
      invalid.reportValidity();
      invalid.focus();
      return;
    }
    status.textContent = "Saving…";
    try {
      await request("/api/config", {
        method: "PUT", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ baseHash: snapshot.fileHash, config: draft }),
      });
      editing = false;
      await load();
    } catch (error) {
      status.textContent = conflictMessage(error.body) || error.message;
      const target = issueControl(error.body?.error?.issues, controls);
      target?.control.focus();
    }
  });
  restartButton.addEventListener("click", async () => {
    if (!confirm(restartMessage(snapshot.restartServices))) return;
    status.textContent = "Restarting services…";
    try {
      const response = await request("/api/config/restart", {
        method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ pendingHash: snapshot.fileHash }),
      });
      beginRestart(response.pendingHash);
    } catch (error) {
      status.textContent = error.message;
    }
  });

  function beginRestart(pendingHash) {
    expectedRestartHash = pendingHash;
    status.textContent = "Restart accepted. Waiting for the controller to reconnect…";
    clearTimeout(restartTimer);
    restartTimer = setTimeout(() => {
      if (status.textContent.startsWith("Restart accepted")) {
        status.textContent = "Restart has not completed. Retry when services are healthy.";
      }
    }, 60_000);
  }

  globalThis.addEventListener?.("hashchange", () => {
    if (!loaded) return;
    const section = sectionForAnchor(globalThis.location?.hash?.slice(1) ?? "");
    if (!section) return;
    selectSection(section.id);
    render();
  });

  return {
    show(page) {
      if (page === "config" && !loaded) load();
    },
    saved() {
      if (!editing) load();
    },
    restarting(message) {
      beginRestart(message?.pendingHash ?? snapshot.fileHash);
    },
    async connection(state) {
      if (state !== "live" || !expectedRestartHash) return;
      try {
        const latest = await request("/api/config");
        if (restartComplete(latest, expectedRestartHash)) {
          snapshot = latest;
          expectedRestartHash = "";
          clearTimeout(restartTimer);
          status.textContent = "";
          render();
        }
      } catch {
        // The normal websocket reconnect schedule will trigger another check.
      }
    },
  };
}
