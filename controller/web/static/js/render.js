import { updateConnectionSnapshot } from "./conn.js";
import { durationElement } from "./duration.js";

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

function gpuName(gpu) {
  const vendor = { nvidia: "NVIDIA", amd: "AMD", intel: "Intel" }[gpu?.vendor] ?? "";
  return [vendor, gpu?.model].filter(Boolean).join(" ");
}

function addressWithPort(address, port) {
  return address.includes(":") ? `[${address}]:${port}` : `${address}:${port}`;
}

export function renderState(snapshot) {
  if (snapshot?.type !== "state" || !Array.isArray(snapshot.services)) {
    return;
  }
  const status = document.querySelector("#status-summary");
  status.className = `overall ${snapshot.ok ? "ok" : "error"}`;
  document.querySelector("#status-title").textContent = snapshot.ok ? "All systems operational" : "Service attention required";
  document.querySelector("#status-detail").textContent = snapshot.ok ? `All ${snapshot.services.length} supervised services are healthy.` : "One or more supervised services are unavailable.";
  document.querySelector("#uptime").replaceChildren(durationElement(snapshot.uptimeSec * 1000));
  document.querySelector("#home-uptime").replaceChildren(durationElement(snapshot.uptimeSec * 1000));
  document.querySelector("#home-host").textContent = snapshot.hostname || "…";
  const port = Number(snapshot.net?.port) || 8080;
  const containerAddresses = (snapshot.net?.addrs ?? []).map((address) => addressWithPort(address, port));
  const homeAddress = document.querySelector("#home-address");
  homeAddress.textContent = location.host;
  homeAddress.title = containerAddresses.join(", ");
  document.querySelector("#home-container-address").textContent = containerAddresses.length
    ? `container: ${containerAddresses.join(", ")}`
    : "container: unavailable";
  const buildVersion = String(snapshot.version || "").trim();
  document.querySelector("#home-version").textContent = buildVersion
    ? `Virtual Me ${buildVersion === "dev" ? "dev" : `v${buildVersion}`}`
    : "Virtual Me";
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
  document.querySelector("#home-cpu").textContent = `${cores} cores`;
  document.querySelector("#home-cpu-load").textContent = `load ${load.toFixed(2)}`;
  document.querySelector("#home-memory").textContent = `${(used / 1024).toFixed(1)} / ${(total / 1024).toFixed(1)} GB`;
  document.querySelector("#home-disk").textContent = `${(diskFree / 1024).toFixed(1)} GB free`;
  document.querySelector("#home-disk-total").textContent = `of ${(diskTotal / 1024).toFixed(1)} GB`;
  const gpu = snapshot.gpu ?? {};
  const name = document.querySelector("#gpu-name");
  const params = document.querySelector("#gpu-params");
  const caption = document.querySelector("#gpu-caption");
  const homeGPURow = document.querySelector("#home-gpu-row");
  if (gpu.present) {
    const displayName = gpuName(gpu);
    name.textContent = displayName;
    name.classList.remove("muted");
    params.replaceChildren(...(gpu.params ?? []).map((param) => {
      const row = document.createElement("div");
      const key = document.createElement("dt");
      const value = document.createElement("dd");
      key.textContent = param.key;
      value.textContent = param.value;
      row.append(key, value);
      return row;
    }));
    params.hidden = false;
    caption.hidden = true;
    document.querySelector("#home-gpu").textContent = displayName;
    homeGPURow.hidden = false;
  } else {
    name.textContent = "none detected";
    name.classList.add("muted");
    params.replaceChildren();
    params.hidden = true;
    caption.hidden = false;
    homeGPURow.hidden = true;
  }
  const scheduler = snapshot.scheduler ?? {};
  const clock = document.querySelector("#scheduler-clock");
  const instant = new Date(scheduler.localTime);
  clock.textContent = Number.isNaN(instant.getTime())
    ? "Waiting for scheduler clock"
    : `${new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "medium" }).format(instant)} · ${scheduler.tz || "local"}`;
  const selectorList = document.querySelector("#scheduler-active");
  selectorList.replaceChildren();
  for (const token of scheduler.active ?? []) {
    const item = document.createElement("li");
    item.textContent = token;
    // Every listed token is currently active; highlight them all.
    item.className = "current";
    selectorList.append(item);
  }
  document.querySelector("#jiggler-switch").setAttribute(
    "aria-checked",
    String(snapshot.jiggler?.enabled === true),
  );
  document.querySelector("#scheduler-switch").setAttribute(
    "aria-checked",
    String(scheduler.paused === true),
  );
  updateConnectionSnapshot(snapshot);
}
