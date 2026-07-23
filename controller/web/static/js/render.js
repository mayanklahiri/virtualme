const iconForService = {
  xvfb: "i-display",
  x11vnc: "i-eye",
  novnc: "i-eye",
  valkey: "i-db",
  llama: "i-chip",
  chromium: "i-globe",
};

function icon(symbol) {
  const template = document.querySelector("#service-icon");
  const svg = template.content.firstElementChild.cloneNode(true);
  const use = svg.querySelector("use");
  use.setAttribute("href", `#${symbol}`);
  return svg;
}

function createService(name) {
  const item = document.createElement("li");
  item.className = "service";
  item.dataset.service = name;
  item.append(icon(iconForService[name] ?? "i-gauge"));

  const body = document.createElement("div");
  const head = document.createElement("div");
  head.className = "service-head";
  const title = document.createElement("h3");
  title.textContent = name;
  const dot = document.createElement("span");
  dot.className = "status-dot";
  head.append(title, dot);
  const detail = document.createElement("p");
  body.append(head, detail);
  item.append(body);
  return item;
}

function formatUptime(seconds) {
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const remainder = Math.floor(seconds % 60);
  return [
    days > 0 ? `${days}d` : "",
    hours > 0 || days > 0 ? `${hours}h` : "",
    minutes > 0 || hours > 0 || days > 0 ? `${minutes}m` : "",
    `${remainder}s`,
  ].filter(Boolean).join(" ");
}

export function renderState(snapshot) {
  if (snapshot?.type !== "state" || !Array.isArray(snapshot.services)) {
    return;
  }
  const status = document.querySelector("#status");
  status.className = `overall ${snapshot.ok ? "ok" : "error"}`;
  document.querySelector("#status-title").textContent = snapshot.ok ? "All systems operational" : "Service attention required";
  document.querySelector("#status-detail").textContent = snapshot.ok ? "All six supervised services are healthy." : "One or more supervised services are unavailable.";
  document.querySelector("#uptime").textContent = formatUptime(snapshot.uptimeSec);

  const list = document.querySelector("#service-list");
  const active = new Set();
  for (const service of snapshot.services) {
    active.add(service.name);
    let item = [...list.children].find((child) => child.dataset.service === service.name);
    if (!item) {
      item = createService(service.name);
    }
    const dot = item.querySelector(".status-dot");
    dot.className = `status-dot ${service.ok ? "ok" : "error"}`;
    dot.setAttribute("aria-label", service.ok ? "healthy" : "unhealthy");
    item.querySelector("p").textContent = service.ok ? "Healthy" : (service.detail || "Unavailable");
    list.append(item);
  }
  for (const item of [...list.children]) {
    if (!active.has(item.dataset.service)) {
      item.remove();
    }
  }

  const system = snapshot.system ?? {};
  const load = Number(system.load1) || 0;
  const used = Number(system.memUsedMB) || 0;
  const total = Number(system.memTotalMB) || 0;
  const loadMeter = document.querySelector("#load");
  loadMeter.value = Math.min(load, Number(loadMeter.max));
  loadMeter.setAttribute("aria-valuenow", String(load));
  document.querySelector("#load-value").textContent = load.toFixed(2);
  const memoryMeter = document.querySelector("#memory");
  memoryMeter.max = Math.max(total, 1);
  memoryMeter.value = Math.min(used, memoryMeter.max);
  memoryMeter.setAttribute("aria-valuenow", String(used));
  document.querySelector("#memory-value").textContent = `${used} / ${total} MB`;
}

export function renderStatus(status) {
  const pill = document.querySelector("#connection");
  pill.className = `connection ${status}`;
  pill.textContent = status === "live" ? "live" : `${status}…`;
}
