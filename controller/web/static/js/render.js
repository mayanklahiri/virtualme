const iconForService = {
  xvfb: "monitor",
  openbox: "monitor",
  x11vnc: "monitor",
  novnc: "monitor",
  valkey: "activity",
  llama: "bot",
  chromium: "monitor",
  controller: "terminal",
};

function icon(symbol) {
  const svg = document.createElementNS("http:" + "//www.w3.org/2000/svg", "svg");
  svg.classList.add("icon");
  svg.setAttribute("aria-hidden", "true");
  const use = document.createElementNS("http:" + "//www.w3.org/2000/svg", "use");
  use.setAttribute("href", `/icons.svg#i-${symbol}`);
  svg.append(use);
  return svg;
}

function createService(name) {
  const item = document.createElement("li");
  item.className = "service";
  item.dataset.service = name;
  item.append(icon(iconForService[name] ?? "activity"));

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
  const status = document.querySelector("#status-summary");
  status.className = `overall ${snapshot.ok ? "ok" : "error"}`;
  document.querySelector("#status-title").textContent = snapshot.ok ? "All systems operational" : "Service attention required";
  document.querySelector("#status-detail").textContent = snapshot.ok ? "All six supervised services are healthy." : "One or more supervised services are unavailable.";
  document.querySelector("#uptime").textContent = formatUptime(snapshot.uptimeSec);
  document.querySelector("#home-uptime").textContent = formatUptime(snapshot.uptimeSec);
  document.querySelector("#home-host").textContent = snapshot.hostname || "—";
  const homeHealth = document.querySelector("#home-health");
  homeHealth.className = `health-pill ${snapshot.ok ? "ok" : "error"}`;
  homeHealth.textContent = snapshot.ok ? "All systems operational" : "Service attention required";

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
  const diskFree = Number(system.diskFreeMB) || 0;
  const diskTotal = Number(system.diskTotalMB) || 0;
  const cores = Array.isArray(snapshot.cores) ? snapshot.cores.length : 0;
  document.querySelector("#home-cpu").textContent = `${cores} cores · load ${load.toFixed(2)}`;
  document.querySelector("#home-memory").textContent = `${(used / 1024).toFixed(1)} / ${(total / 1024).toFixed(1)} GB`;
  document.querySelector("#home-disk").textContent = `${(diskFree / 1024).toFixed(1)} GB free of ${(diskTotal / 1024).toFixed(1)} GB`;
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
  for (const pill of document.querySelectorAll("[data-connection]")) {
    pill.className = `connection ${status}`;
    pill.textContent = status === "live" ? "live" : `${status}…`;
  }
}
