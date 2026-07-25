const themes = ["modern", "editorial", "terminal", "warm", "contrast", "arctic", "solar", "studio"];
const variants = ["auto", "light", "dark"];

export function initializeThemePicker() {
  const root = document.documentElement;
  const toggle = document.querySelector<HTMLButtonElement>(".theme-toggle");
  const panel = document.querySelector<HTMLElement>("#theme-panel");
  const scheme = matchMedia("(prefers-color-scheme: dark)");
  let savedTheme = ""; let savedVariant = "";
  try { savedTheme = localStorage.getItem("vm-theme") ?? ""; savedVariant = localStorage.getItem("vm-variant") ?? ""; } catch {}
  let theme = themes.includes(savedTheme) ? savedTheme : "modern";
  let variant = variants.includes(savedVariant) ? savedVariant : "auto";
  const apply = () => {
    root.dataset.theme = theme;
    root.dataset.variant = variant === "auto" ? (scheme.matches ? "dark" : "light") : variant;
    document.querySelectorAll<HTMLElement>("[data-theme-value]").forEach((button) => button.setAttribute("aria-pressed", String(button.dataset.themeValue === theme)));
    document.querySelectorAll<HTMLElement>("[data-variant-value]").forEach((button) => button.setAttribute("aria-pressed", String(button.dataset.variantValue === variant)));
  };
  const close = () => { if (panel) panel.hidden = true; toggle?.setAttribute("aria-expanded", "false"); };
  toggle?.addEventListener("click", () => {
    if (!panel) return;
    panel.hidden = !panel.hidden;
    toggle.setAttribute("aria-expanded", String(!panel.hidden));
    if (!panel.hidden) panel.querySelector<HTMLButtonElement>("[aria-pressed=true]")?.focus();
  });
  document.querySelectorAll<HTMLButtonElement>("[data-theme-value]").forEach((button) => button.addEventListener("click", () => {
    theme = button.dataset.themeValue!;
    try { localStorage.setItem("vm-theme", theme); } catch {}
    apply(); close();
  }));
  document.querySelectorAll<HTMLButtonElement>("[data-variant-value]").forEach((button) => button.addEventListener("click", () => {
    variant = button.dataset.variantValue!;
    try { localStorage.setItem("vm-variant", variant); } catch {}
    apply();
  }));
  scheme.addEventListener("change", () => { if (variant === "auto") apply(); });
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && panel && !panel.hidden) { close(); toggle?.focus(); }
  });
  document.addEventListener("click", (event) => { if (!panel?.contains(event.target as Node) && !toggle?.contains(event.target as Node)) close(); });
  apply();
}
