import { formatShortDuration } from "./duration.js";

let connectionStatus = "connecting";
let connectedSince = 0;
let latestSnapshot = null;

function renderText(now = Date.now()) {
  for (const watch of document.querySelectorAll("[data-connection]")) {
    const host = watch.querySelector(".conn-host");
    const meta = watch.querySelector(".conn-meta");
    host.textContent = latestSnapshot
      ? `${latestSnapshot.hostname || "virtualme"}:${latestSnapshot.net?.port || 8080}`
      : location.host;
    if (connectionStatus === "live") {
      const linked = connectedSince ? now - connectedSince : 0;
      const uptime = (Number(latestSnapshot?.uptimeSec) || 0) * 1000;
      meta.textContent = `up ${formatShortDuration(uptime)} · linked ${formatShortDuration(linked)}`;
    } else {
      meta.textContent = `${connectionStatus}…`;
    }
  }
}

export function initConnectionWatch() {
  renderText();
  setInterval(() => renderText(), 1000);
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
  renderText();
}
