// The one human-short duration formatter for the console (spec 026 U1).
// Renders the top two nonzero units of d/h/m/s; durationElement() grades
// brightness per unit (days/hours brightest, seconds dim) via CSS classes.

/**
 * @param {number} ms
 * @returns {{unit: "d"|"h"|"m"|"s", text: string}[]}
 */
export function durationParts(ms) {
  if (!Number.isFinite(ms) || ms <= 0) {
    return [{ unit: "s", text: "0s" }];
  }
  if (ms < 1000) {
    const rounded = (ms / 1000).toFixed(1);
    return [{ unit: "s", text: rounded === "0.0" ? "0s" : `${rounded}s` }];
  }
  const total = Math.floor(ms / 1000);
  /** @type {{unit: "d"|"h"|"m"|"s", value: number}[]} */
  const units = [
    { unit: "d", value: Math.floor(total / 86400) },
    { unit: "h", value: Math.floor(total / 3600) % 24 },
    { unit: "m", value: Math.floor(total / 60) % 60 },
    { unit: "s", value: total % 60 },
  ];
  const parts = units
    .filter((part) => part.value > 0)
    .slice(0, 2)
    .map((part) => ({ unit: part.unit, text: `${part.value}${part.unit}` }));
  return parts.length ? parts : [{ unit: "s", text: "0s" }];
}

/** @param {number} ms */
export function formatShortDuration(ms) {
  return durationParts(ms).map((part) => part.text).join(" ");
}

/**
 * Builds a `<span class="dur">` with one graded `<span>` per unit.
 * @param {number} ms
 */
export function durationElement(ms) {
  const wrap = document.createElement("span");
  wrap.className = "dur";
  const parts = durationParts(ms);
  parts.forEach((part, index) => {
    if (index > 0) {
      wrap.append(document.createTextNode(" "));
    }
    const node = document.createElement("span");
    node.className = `dur-${part.unit}`;
    node.textContent = part.text;
    wrap.append(node);
  });
  return wrap;
}
