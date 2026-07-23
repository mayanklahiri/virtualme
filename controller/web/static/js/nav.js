export function initNav() {
  const drawer = document.querySelector("#sidebar");
  const curtain = document.querySelector("#nav-curtain");
  const openButton = document.querySelector("#nav-open");
  const closeButton = document.querySelector("#nav-close");
  let previousFocus;

  function focusables() {
    return [...drawer.querySelectorAll("a[href], button:not([disabled])")];
  }
  function close() {
    drawer.classList.remove("open");
    curtain.classList.remove("open");
    curtain.hidden = true;
    document.body.classList.remove("nav-locked");
    openButton.setAttribute("aria-expanded", "false");
    previousFocus?.focus();
  }
  function open() {
    previousFocus = document.activeElement;
    curtain.hidden = false;
    requestAnimationFrame(() => {
      drawer.classList.add("open");
      curtain.classList.add("open");
    });
    document.body.classList.add("nav-locked");
    openButton.setAttribute("aria-expanded", "true");
    focusables()[0]?.focus();
  }
  openButton.addEventListener("click", open);
  closeButton.addEventListener("click", close);
  curtain.addEventListener("click", close);
  drawer.addEventListener("click", (event) => {
    if (event.target.closest?.("a[data-nav]") && matchMedia("(max-width: 47.999rem)").matches) {
      close();
    }
  });
  drawer.addEventListener("keydown", (event) => {
    if (event.key === "Escape") {
      event.preventDefault();
      close();
    } else if (event.key === "Tab" && drawer.classList.contains("open")) {
      const items = focusables();
      const first = items[0];
      const last = items.at(-1);
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    }
  });
  return { close };
}
