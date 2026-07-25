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

export function parseEditorValue(value, setting) {
  if (setting.type === "array") {
    if (!Array.isArray(value)) throw new Error("Enter ordered list rows.");
    if (setting.constraints?.uniqueItems && new Set(value).size !== value.length) {
      throw new Error("List rows must be unique.");
    }
    return value.map((item) => String(item));
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

export function validateSecretReference(value) {
  return value === "" ||
    /^\$\{env:[A-Z][A-Z0-9_]*\}$/.test(value) ||
    /^\$\{file:\/[^{}]*\}$/.test(value);
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
