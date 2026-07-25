export function initializeSnowglobe() {
  const figure = document.querySelector<HTMLElement>("[data-snowglobe]");
  if (!figure || figure.dataset.ready) return;
  figure.dataset.ready = "true";
  const globe = figure.querySelector<HTMLElement>(".globe");
  const link = figure.querySelector<HTMLElement>("[data-about-link]");
  const caption = figure.querySelector<HTMLElement>("[data-globe-caption]");
  let timer: number | undefined;
  const reveal = () => {
    figure.classList.remove("shaking");
    void figure.offsetWidth;
    figure.classList.add("shaking");
    if (link) link.hidden = false;
    if (caption) caption.textContent = "A memory drawer opened.";
    clearTimeout(timer);
    timer = window.setTimeout(() => figure.classList.remove("shaking"), 5800);
  };
  figure.querySelector("[data-shake]")?.addEventListener("click", reveal);
  globe?.addEventListener("focus", reveal);
  globe?.addEventListener("pointermove", (event) => {
    if (matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    const box = globe.getBoundingClientRect();
    const x = ((event.clientX - box.left) / box.width - 0.5) * 8;
    const y = ((event.clientY - box.top) / box.height - 0.5) * -8;
    globe.style.transform = `rotateX(${y}deg) rotateY(${x}deg)`;
  });
  globe?.addEventListener("pointerleave", () => { globe.style.transform = ""; });
}
