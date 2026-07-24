const routes = new Map([
  ["/", ["home", "Home"]],
  ["/projects", ["projects", "Projects"]],
  ["/status", ["status", "Status"]],
  ["/chat", ["chat", "Chat"]],
  ["/speech", ["speech", "Speech"]],
  ["/mail", ["mail", "Mail"]],
  ["/desktop-view", ["desktop", "Desktop"]],
]);

export function initRouter(onNavigate = () => {}) {
  function render() {
    const projectDetail = location.pathname.startsWith("/projects/") && location.pathname.length > 10;
    const [page, title] = projectDetail
      ? ["project-detail", "Project"]
      : (routes.get(location.pathname) ?? routes.get("/"));
    for (const section of document.querySelectorAll("[data-page]")) {
      section.hidden = section.dataset.page !== page;
    }
    for (const link of document.querySelectorAll("a[data-nav]")) {
      const linkPath = new URL(link.href).pathname;
      const currentPath = routes.has(location.pathname) ? location.pathname : (projectDetail ? "/projects" : "/");
      if (linkPath === currentPath) {
        link.setAttribute("aria-current", "page");
      } else {
        link.removeAttribute("aria-current");
      }
    }
    document.querySelector("#page-title").textContent = title;
    document.title = `${title} · Virtual Me`;
    onNavigate(page);
  }

  document.addEventListener("click", (event) => {
    const link = event.target.closest?.("a[data-nav]");
    if (!link || event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) {
      return;
    }
    const url = new URL(link.href, location.href);
    if (url.origin !== location.origin) {
      return;
    }
    event.preventDefault();
    history.pushState(null, "", url.pathname);
    render();
  });
  addEventListener("popstate", render);
  render();
}
