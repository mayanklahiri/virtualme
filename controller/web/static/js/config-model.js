// @ts-nocheck
export function configAnchor(path) {
  return path
    .replaceAll(".", "-")
    .replace(/([a-z0-9])([A-Z])/g, "$1-$2")
    .toLowerCase();
}

export function orderedSections(sections) {
  return [...sections].sort((left, right) =>
    (left.ui?.order ?? left.order ?? 0) - (right.ui?.order ?? right.order ?? 0) ||
    String(left.id).localeCompare(String(right.id)));
}

export function orderedSettings(settings) {
  return [...settings].sort((left, right) =>
    (left.ui?.order ?? 0) - (right.ui?.order ?? 0) ||
    left.path.localeCompare(right.path));
}

/** @param {any[]} settings */
export function buildSettingTree(settings) {
  const root = { name: "", depth: 0, children: new Map(), settings: [] };
  for (const setting of orderedSettings(settings)) {
    const parts = setting.path.split(".");
    let node = root;
    for (let index = 0; index < parts.length - 1; index++) {
      const part = parts[index];
      if (!node.children.has(part)) {
        node.children.set(part, { name: part, depth: index + 1, children: new Map(), settings: [] });
      }
      node = node.children.get(part);
    }
    node.settings.push(setting);
  }
  return root;
}

export function parseEditorValue(value, setting) {
  if (setting.type === "array") {
    if (!Array.isArray(value)) throw new Error("Enter ordered list rows.");
    if (setting.constraints?.uniqueItems && new Set(value).size !== value.length) {
      throw new Error("List rows must be unique.");
    }
    return value.map((item) => validateStringItem(String(item), setting.item));
  }
  if (setting.type === "integer") {
    if (!/^-?(0|[1-9][0-9]*)$/.test(String(value))) {
      throw new Error("Enter a whole number.");
    }
    return Number.parseInt(String(value), 10);
  }
  if (setting.type === "number") {
    const result = Number(value);
    if (!Number.isFinite(result)) throw new Error("Enter a finite number.");
    return result;
  }
  if (setting.type === "boolean") return value === true || value === "true";
  return String(value);
}

export function validateStringItem(value, item) {
  if (!item || item.type !== "string") return value;
  const rules = item.constraints ?? {};
  const length = [...value].length;
  if (rules.minLength !== undefined && length < rules.minLength) {
    throw new Error(`List row must contain at least ${rules.minLength} characters.`);
  }
  if (rules.maxLength !== undefined && length > rules.maxLength) {
    throw new Error(`List row must contain at most ${rules.maxLength} characters.`);
  }
  if (rules.pattern && !new RegExp(rules.pattern).test(value)) {
    throw new Error("List row does not match the required pattern.");
  }
  return value;
}

export function validateSecretReference(value) {
  return value === "" ||
    /^\$\{env:[A-Z][A-Z0-9_]*\}$/.test(value) ||
    /^\$\{file:\/[^{}]*\}$/.test(value);
}

export function secretStatusLabel(secret) {
  if (!secret?.configured) return "Not configured";
  if (secret.status === "inactive") return "Inactive";
  if (secret.resolved) return "Resolved";
  return secret.error ? `Unavailable (${secret.error})` : "Unresolved";
}

export function setConfigPath(root, dotted, value) {
  const parts = dotted.split(".");
  let current = root;
  for (const part of parts.slice(0, -1)) current = current[part];
  current[parts.at(-1)] = value;
}

export function getConfigPath(root, dotted) {
  return dotted.split(".").reduce((current, part) => current?.[part], root);
}

export function cloneConfig(value) {
  return structuredClone(value);
}

export function issueControl(issues, controls) {
  for (const issue of issues ?? []) {
    const control = controls.get(issue.path);
    if (control) return { issue, control };
  }
  return null;
}

export function restartMessage(services) {
  return `Restart ${services.join(", ")}. Active chat, tool, and speech work will disconnect.`;
}

export function conflictMessage(response) {
  return response?.error?.code === "config_conflict"
    ? "Configuration changed in another session. Reload before saving."
    : "";
}

export function advancedGroups(settings) {
  return {
    regular: settings.filter((setting) => !setting.ui?.advanced),
    advanced: settings.filter((setting) => setting.ui?.advanced),
  };
}

export function restartComplete(snapshot, expectedHash) {
  return snapshot?.fileHash === expectedHash &&
    snapshot?.startupHash === expectedHash &&
    snapshot?.pendingRestart === false;
}
