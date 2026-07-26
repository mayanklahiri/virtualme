import { themes } from "./generated-themes.js";
const themeIds = themes.map(({ id }) => id);
const variants = [
  ["auto", "palette"],
  ["light", "sun"],
  ["dark", "moon"],
];
const scheme = matchMedia("(prefers-color-scheme: dark)");

function icon(name) {
  const svg = document.createElementNS("http:" + "//www.w3.org/2000/svg", "svg");
  svg.classList.add("icon");
  svg.setAttribute("aria-hidden", "true");
  const use = document.createElementNS("http:" + "//www.w3.org/2000/svg", "use");
  use.setAttribute("href", `/icons.svg#i-${name}`);
  svg.append(use);
  return svg;
}

export function initTheme() {
  const themeBox = document.querySelector("#theme-options");
  const variantBox = document.querySelector("#variant-options");
  const themeButton = document.querySelector("#theme-button");
  const themeCurrent = document.querySelector("#theme-current");
  const popover = document.querySelector("#theme-popover");
  const savedTheme = localStorage.getItem("vm-theme");
  const savedVariant = localStorage.getItem("vm-variant");
  let theme = themeIds.includes(savedTheme) ? savedTheme : "modern";
  let preference = variants.some(([name]) => name === savedVariant) ? savedVariant : "auto";

  function closePopover(restore = true) {
    if (popover.hidden) {
      return;
    }
    popover.hidden = true;
    themeButton.setAttribute("aria-expanded", "false");
    if (restore && popover.contains(document.activeElement)) {
      themeButton.focus();
    }
  }

  function togglePopover() {
    const opening = popover.hidden;
    if (opening) {
      dispatchEvent(new CustomEvent("themepopoveropen"));
    }
    popover.hidden = !opening;
    themeButton.setAttribute("aria-expanded", String(opening));
    if (opening) {
      themeBox.querySelector(`[data-value="${theme}"]`)?.focus();
    }
  }

  function apply() {
    const resolved = preference === "auto" ? (scheme.matches ? "dark" : "light") : preference;
    document.documentElement.dataset.theme = theme;
    document.documentElement.dataset.variant = resolved;
    for (const button of themeBox.children) {
      button.setAttribute("aria-pressed", String(button.dataset.value === theme));
    }
    for (const button of variantBox.children) {
      button.setAttribute("aria-pressed", String(button.dataset.value === preference));
    }
    themeCurrent.textContent = theme[0].toUpperCase() + theme.slice(1);
    dispatchEvent(new CustomEvent("themechange"));
  }

  themeButton.addEventListener("click", togglePopover);
  addEventListener("notificationpopoveropen", () => closePopover(false));
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && !popover.hidden) {
      closePopover();
    }
  });
  document.addEventListener("click", (event) => {
    if (!popover.hidden && !popover.contains(event.target) && !themeButton.contains(event.target)) {
      closePopover();
    }
  });

  for (const { id: name, label: themeLabel } of themes) {
    const button = document.createElement("button");
    button.type = "button";
    button.dataset.value = name;
    button.className = `theme-swatch theme-${name}`;
    const swatch = document.createElement("span");
    swatch.setAttribute("aria-hidden", "true");
    const label = document.createElement("span");
    label.textContent = themeLabel;
    button.append(swatch, label);
    button.addEventListener("click", () => {
      theme = name;
      localStorage.setItem("vm-theme", theme);
      apply();
      closePopover();
    });
    themeBox.append(button);
  }
  for (const [name, iconName] of variants) {
    const button = document.createElement("button");
    button.type = "button";
    button.dataset.value = name;
    button.setAttribute("aria-label", `${name} color variant`);
    button.append(icon(iconName), document.createTextNode(name));
    button.addEventListener("click", () => {
      preference = name;
      localStorage.setItem("vm-variant", preference);
      apply();
    });
    variantBox.append(button);
  }
  scheme.addEventListener("change", () => {
    if (preference === "auto") {
      apply();
    }
  });
  apply();
}
