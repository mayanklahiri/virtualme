const PROCESSES = ["xvfb", "openbox", "x11vnc", "novnc", "valkey", "llama", "chromium", "controller"];
const LOOKBACKS = ["15m", "1h", "3h", "12h", "1d", "3d", "7d", "30d"];
const SPANS = { "15m": 900e3, "1h": 3600e3, "3h": 10800e3, "12h": 43200e3, "1d": 86400e3, "3d": 259200e3, "7d": 604800e3, "30d": 2592000e3 };
const PAD = { left: 52, right: 8, top: 8, bottom: 24 };

function metricSample(snapshot) {
  const byName = new Map((snapshot.processes ?? []).map((process) => [process.name, process]));
  return {
    ts: Number(snapshot.ts),
    cores: (snapshot.cores ?? []).map(Number),
    procMemMB: PROCESSES.map((name) => Number(byName.get(name)?.memMB) || 0),
  };
}

function css(name) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}

export function initCharts(send) {
  const cpuCanvas = document.querySelector("#chart-cpu");
  const memCanvas = document.querySelector("#chart-mem");
  const tooltip = document.querySelector("#chart-tooltip");
  const lookbackBox = document.querySelector("#lookback");
  const cpuLegend = document.querySelector("#core-legend");
  const memLegend = document.querySelector("#process-legend");
  let lookback = LOOKBACKS.includes(localStorage.getItem("vm-lookback")) ? localStorage.getItem("vm-lookback") : "1h";
  let samples = [];
  let resSec = 2;
  let live = false;
  let hover = -1;

  function request() {
    if (live) {
      send({ type: "metrics-req", lookback });
    }
  }
  function select(value) {
    lookback = value;
    localStorage.setItem("vm-lookback", value);
    for (const button of lookbackBox.children) {
      button.setAttribute("aria-checked", String(button.dataset.value === value));
      button.tabIndex = button.dataset.value === value ? 0 : -1;
    }
    request();
  }
  for (const value of LOOKBACKS) {
    const button = document.createElement("button");
    button.type = "button";
    button.role = "radio";
    button.dataset.value = value;
    button.textContent = value;
    button.addEventListener("click", () => select(value));
    button.addEventListener("keydown", (event) => {
      if (!["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown"].includes(event.key)) {
        return;
      }
      event.preventDefault();
      const step = event.key === "ArrowLeft" || event.key === "ArrowUp" ? -1 : 1;
      const next = (LOOKBACKS.indexOf(lookback) + step + LOOKBACKS.length) % LOOKBACKS.length;
      select(LOOKBACKS[next]);
      lookbackBox.children[next].focus();
    });
    lookbackBox.append(button);
  }
  select(lookback);

  function updateLegends() {
    const coreCount = Math.max(0, ...samples.map((sample) => sample.cores.length));
    cpuLegend.replaceChildren(...Array.from({ length: coreCount }, (_, index) => {
      const item = document.createElement("li");
      const swatch = document.createElement("span");
      swatch.className = "swatch core";
      swatch.style.opacity = String(0.3 + (index % 5) * 0.14);
      item.append(swatch, `cpu${index}`);
      return item;
    }));
    memLegend.replaceChildren(...PROCESSES.map((name, index) => {
      const item = document.createElement("li");
      const swatch = document.createElement("span");
      swatch.className = "swatch";
      swatch.style.background = css(`--p${index + 1}`);
      item.append(swatch, name);
      return item;
    }));
  }

  function setup(canvas) {
    const rect = canvas.getBoundingClientRect();
    const ratio = devicePixelRatio || 1;
    const width = Math.max(rect.width, 100);
    const height = Math.max(rect.height, 100);
    if (canvas.width !== Math.round(width * ratio) || canvas.height !== Math.round(height * ratio)) {
      canvas.width = Math.round(width * ratio);
      canvas.height = Math.round(height * ratio);
    }
    const context = canvas.getContext("2d");
    context.setTransform(ratio, 0, 0, ratio, 0, 0);
    context.clearRect(0, 0, width, height);
    return { context, width, height };
  }

  function axes(context, width, height, max, unit) {
    const plotHeight = height - PAD.top - PAD.bottom;
    context.font = `10px ${css("--font-body")}`;
    context.strokeStyle = css("--border");
    context.fillStyle = css("--muted");
    context.lineWidth = 1;
    for (let line = 0; line <= 4; line++) {
      const y = PAD.top + plotHeight - plotHeight * line / 4;
      context.beginPath();
      context.moveTo(PAD.left, y);
      context.lineTo(width - PAD.right, y);
      context.stroke();
      context.textAlign = "right";
      context.textBaseline = "middle";
      context.fillText(`${Math.round(max * line / 4)}${unit}`, PAD.left - 5, y);
    }
  }

  function scales(width, height, max) {
    const first = samples[0]?.ts ?? 0;
    const last = samples.at(-1)?.ts ?? first + 1;
    const span = Math.max(last - first, 1);
    return {
      x: (ts) => PAD.left + (ts - first) / span * (width - PAD.left - PAD.right),
      y: (value) => PAD.top + (1 - value / Math.max(max, 1)) * (height - PAD.top - PAD.bottom),
    };
  }

  function segments() {
    const result = [];
    let start = 0;
    for (let i = 1; i <= samples.length; i++) {
      if (i === samples.length || samples[i].ts - samples[i - 1].ts > resSec * 3000) {
        if (i > start) result.push(samples.slice(start, i));
        start = i;
      }
    }
    return result;
  }

  function drawCPU() {
    const { context, width, height } = setup(cpuCanvas);
    const coreCount = Math.max(0, ...samples.map((sample) => sample.cores.length));
    const max = Math.max(coreCount * 100, 100);
    axes(context, width, height, max, "%");
    const scale = scales(width, height, max);
    context.fillStyle = css("--accent");
    for (let core = 0; core < coreCount; core++) {
      context.globalAlpha = 0.3 + (core % 5) * 0.14;
      for (const part of segments()) {
        context.beginPath();
        part.forEach((sample, index) => {
          const top = sample.cores.slice(0, core + 1).reduce((sum, value) => sum + (value || 0), 0);
          const x = scale.x(sample.ts);
          const y = scale.y(top);
          if (index === 0) context.moveTo(x, y); else context.lineTo(x, y);
        });
        for (let index = part.length - 1; index >= 0; index--) {
          const sample = part[index];
          const base = sample.cores.slice(0, core).reduce((sum, value) => sum + (value || 0), 0);
          context.lineTo(scale.x(sample.ts), scale.y(base));
        }
        context.closePath();
        context.fill();
      }
    }
    context.globalAlpha = 1;
  }

  function drawMemory() {
    const { context, width, height } = setup(memCanvas);
    const max = Math.max(1, ...samples.flatMap((sample) => sample.procMemMB));
    axes(context, width, height, max, "");
    const scale = scales(width, height, max);
    for (let process = 0; process < PROCESSES.length; process++) {
      context.strokeStyle = css(`--p${process + 1}`);
      context.lineWidth = 1.5;
      for (const part of segments()) {
        context.beginPath();
        part.forEach((sample, index) => {
          const x = scale.x(sample.ts);
          const y = scale.y(sample.procMemMB[process] || 0);
          if (index === 0) context.moveTo(x, y); else context.lineTo(x, y);
        });
        context.stroke();
      }
    }
  }

  function draw() {
    updateLegends();
    drawCPU();
    drawMemory();
  }

  function show(index, clientX, clientY) {
    hover = index;
    const sample = samples[index];
    if (!sample) {
      tooltip.hidden = true;
      return;
    }
    tooltip.textContent = [
      new Date(sample.ts).toLocaleString(),
      ...sample.cores.map((value, i) => `cpu${i}: ${value.toFixed(1)}%`),
      ...sample.procMemMB.map((value, i) => `${PROCESSES[i]}: ${value} MiB`),
    ].join("\n");
    tooltip.hidden = false;
    tooltip.style.left = `${Math.min(clientX + 12, innerWidth - tooltip.offsetWidth - 8)}px`;
    tooltip.style.top = `${Math.min(clientY + 12, innerHeight - tooltip.offsetHeight - 8)}px`;
  }
  for (const canvas of [cpuCanvas, memCanvas]) {
    canvas.addEventListener("mousemove", (event) => {
      const rect = canvas.getBoundingClientRect();
      const ratio = Math.max(0, Math.min(1, (event.clientX - rect.left - PAD.left) / Math.max(rect.width - PAD.left - PAD.right, 1)));
      show(Math.round(ratio * Math.max(samples.length - 1, 0)), event.clientX, event.clientY);
    });
    canvas.addEventListener("mouseleave", () => show(-1, 0, 0));
    canvas.addEventListener("keydown", (event) => {
      if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
        event.preventDefault();
        const next = hover < 0 ? samples.length - 1 : hover + (event.key === "ArrowLeft" ? -1 : 1);
        const rect = canvas.getBoundingClientRect();
        show(Math.max(0, Math.min(samples.length - 1, next)), rect.left + PAD.left, rect.bottom);
      } else if (event.key === "Escape") {
        show(-1, 0, 0);
      }
    });
  }
  addEventListener("resize", draw);
  addEventListener("themechange", draw);
  setInterval(() => {
    if (!["15m", "1h"].includes(lookback)) request();
  }, 30000);

  return {
    status(status) {
      live = status === "live";
      if (live) request();
    },
    replace(message) {
      if (message.lookback !== lookback) return;
      resSec = Number(message.resSec) || 2;
      samples = (message.samples ?? []).map((sample) => ({
        ts: Number(sample.ts), cores: (sample.cores ?? []).map(Number),
        procMemMB: (sample.procMemMB ?? []).map(Number),
      }));
      draw();
    },
    push(snapshot) {
      if (!["15m", "1h"].includes(lookback)) return;
      samples.push(metricSample(snapshot));
      const cutoff = Date.now() - SPANS[lookback];
      while (samples[0]?.ts < cutoff) samples.shift();
      resSec = 2;
      draw();
    },
    draw,
  };
}
