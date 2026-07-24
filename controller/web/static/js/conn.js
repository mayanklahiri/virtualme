const SVG_NS = "http:" + "//www.w3.org/2000/svg";
const circumference = 2 * Math.PI * 16;
let connectionStatus = "connecting";
let connectedSince = 0;
let latestSnapshot = null;
let lastSecond = -1;

function duration(seconds, includeSeconds = false) {
  const value = Math.max(0, Math.floor(Number(seconds) || 0));
  const days = Math.floor(value / 86400);
  const hours = Math.floor((value % 86400) / 3600);
  const minutes = Math.floor((value % 3600) / 60);
  const remainder = value % 60;
  const parts = [];
  if (days) parts.push(`${days}d`);
  if (hours) parts.push(`${hours}h`);
  if (minutes) parts.push(`${minutes}m`);
  if (includeSeconds || parts.length === 0) parts.push(`${remainder}s`);
  return parts.join(" ");
}

function ensureTicks(watch) {
  const group = watch.querySelector(".dial-ticks");
  if (group.childElementCount > 0) return;
  for (let index = 0; index < 12; index += 1) {
    const angle = index * Math.PI / 6;
    const line = document.createElementNS(SVG_NS, "line");
    line.setAttribute("x1", (22 + Math.sin(angle) * 16.8).toFixed(1));
    line.setAttribute("y1", (22 - Math.cos(angle) * 16.8).toFixed(1));
    line.setAttribute("x2", (22 + Math.sin(angle) * 18.4).toFixed(1));
    line.setAttribute("y2", (22 - Math.cos(angle) * 18.4).toFixed(1));
    group.append(line);
  }
}

function renderText(now = Date.now()) {
  for (const watch of document.querySelectorAll("[data-connection]")) {
    const host = watch.querySelector(".conn-host");
    const meta = watch.querySelector(".conn-meta");
    host.textContent = latestSnapshot
      ? `${latestSnapshot.hostname || "virtualme"}:${latestSnapshot.net?.port || 8080}`
      : location.host;
    if (connectionStatus === "live") {
      const linked = connectedSince ? (now - connectedSince) / 1000 : 0;
      meta.textContent = `up ${duration(latestSnapshot?.uptimeSec)} · linked ${duration(linked)}`;
    } else {
      meta.textContent = `${connectionStatus}…`;
    }
  }
}

function renderUptime() {
  const uptime = Math.max(0, Number(latestSnapshot?.uptimeSec) || 0);
  const filled = circumference * ((uptime % 86400) / 86400);
  for (const ring of document.querySelectorAll(".dial-uptime")) {
    ring.setAttribute("stroke-dasharray", `${filled.toFixed(2)} ${(circumference - filled).toFixed(2)}`);
    let title = ring.querySelector("title");
    if (!title) {
      title = document.createElementNS(SVG_NS, "title");
      ring.append(title);
    }
    title.textContent = `Server uptime: ${duration(uptime, true)}`;
  }
}

function frame(now) {
  const second = Math.floor(now / 1000);
  if (second !== lastSecond) {
    lastSecond = second;
    renderText(Date.now());
    if (connectionStatus === "live" && !matchMedia("(prefers-reduced-motion: reduce)").matches) {
      const angle = (Date.now() % 60000) / 60000 * 360;
      for (const hand of document.querySelectorAll(".dial-hand")) {
        hand.style.transform = `rotate(${angle.toFixed(1)}deg)`;
      }
    }
  }
  requestAnimationFrame(frame);
}

export function initConnectionWatch() {
  for (const watch of document.querySelectorAll("[data-connection]")) ensureTicks(watch);
  renderText();
  requestAnimationFrame(frame);
  return {
    status(status, since = 0) {
      connectionStatus = status;
      connectedSince = since;
      for (const watch of document.querySelectorAll("[data-connection]")) {
        watch.className = `conn-watch ${status}`;
      }
      renderText();
    },
  };
}

export function updateConnectionSnapshot(snapshot) {
  latestSnapshot = snapshot;
  renderUptime();
  renderText();
}
