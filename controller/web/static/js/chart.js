const NAMES = ["xvfb", "openbox", "x11vnc", "novnc", "valkey", "llama", "chromium", "controller"];
const WINDOW = 150;
const BAR_W = 3;
const GROUP_GAP = 4;
const PAD_LEFT = 44;
const PAD_BOTTOM = 20;
const PAD_TOP = 6;

function readPalette() {
  const style = getComputedStyle(document.documentElement);
  return NAMES.map((_, index) => style.getPropertyValue(`--p${index + 1}`).trim() || "#888");
}

function extract(snapshot) {
  const byName = new Map();
  for (const proc of snapshot.processes ?? []) {
    byName.set(proc.name, proc);
  }
  return {
    ts: snapshot.ts,
    cpu: NAMES.map((name) => Number(byName.get(name)?.cpuPct) || 0),
    mem: NAMES.map((name) => Number(byName.get(name)?.memMB) || 0),
  };
}

export function initCharts() {
  const tooltip = document.querySelector("#chart-tooltip");
  const legend = document.querySelector("#chart-legend");
  const samples = [];
  let colors = readPalette();
  const matchDark = window.matchMedia("(prefers-color-scheme: dark)");
  matchDark.addEventListener("change", () => {
    colors = readPalette();
    buildLegend();
    drawAll();
  });

  function buildLegend() {
    legend.replaceChildren(...NAMES.map((name, index) => {
      const item = document.createElement("li");
      const swatch = document.createElement("span");
      swatch.className = "swatch";
      swatch.style.background = colors[index];
      item.append(swatch, document.createTextNode(name));
      return item;
    }));
  }
  buildLegend();

  const charts = [
    makeChart(document.querySelector("#chart-cpu"), (s) => s.cpu, "%", 100),
    makeChart(document.querySelector("#chart-mem"), (s) => s.mem, " MiB", 1),
  ];

  function makeChart(canvas, pick, unit, minMax) {
    const context = canvas.getContext("2d");
    let hover = -1; // index into the visible slice, from its start

    function visible() {
      const rect = canvas.getBoundingClientRect();
      const plotWidth = Math.max(rect.width - PAD_LEFT, 10);
      const groupWidth = NAMES.length * BAR_W + GROUP_GAP;
      const count = Math.max(Math.floor(plotWidth / groupWidth), 1);
      return { rect, groupWidth, slice: samples.slice(-count) };
    }

    function draw() {
      const { rect, groupWidth, slice } = visible();
      const ratio = window.devicePixelRatio || 1;
      if (canvas.width !== Math.round(rect.width * ratio)) {
        canvas.width = Math.round(rect.width * ratio);
        canvas.height = Math.round(rect.height * ratio);
      }
      context.setTransform(ratio, 0, 0, ratio, 0, 0);
      context.clearRect(0, 0, rect.width, rect.height);
      const style = getComputedStyle(document.documentElement);
      const mutedColor = style.getPropertyValue("--muted").trim();
      const borderColor = style.getPropertyValue("--border").trim();
      const plotHeight = rect.height - PAD_TOP - PAD_BOTTOM;

      let max = minMax;
      for (const sample of slice) {
        for (const value of pick(sample)) {
          if (value > max) {
            max = value;
          }
        }
      }

      context.font = "10px InterVariable, system-ui, sans-serif";
      context.strokeStyle = borderColor;
      context.fillStyle = mutedColor;
      context.lineWidth = 1;
      for (let line = 0; line <= 4; line += 1) {
        const y = PAD_TOP + plotHeight - (plotHeight * line) / 4;
        context.beginPath();
        context.moveTo(PAD_LEFT, y);
        context.lineTo(rect.width, y);
        context.stroke();
        context.textAlign = "right";
        context.textBaseline = "middle";
        context.fillText(Math.round((max * line) / 4) + unit, PAD_LEFT - 5, y);
      }

      slice.forEach((sample, groupIndex) => {
        const groupX = PAD_LEFT + groupIndex * groupWidth;
        if (groupIndex === hover) {
          context.fillStyle = borderColor;
          context.fillRect(groupX - 1, PAD_TOP, groupWidth - GROUP_GAP + 2, plotHeight);
        }
        pick(sample).forEach((value, procIndex) => {
          const height = Math.min(value / max, 1) * plotHeight;
          context.fillStyle = colors[procIndex];
          context.fillRect(groupX + procIndex * BAR_W, PAD_TOP + plotHeight - height, BAR_W - 0.5, height);
        });
      });

      if (slice.length > 0) {
        const first = new Date(slice[0].ts).toLocaleTimeString();
        const last = new Date(slice[slice.length - 1].ts).toLocaleTimeString();
        context.fillStyle = mutedColor;
        context.textBaseline = "top";
        context.textAlign = "left";
        context.fillText(first, PAD_LEFT, rect.height - PAD_BOTTOM + 6);
        context.textAlign = "right";
        context.fillText(last, rect.width, rect.height - PAD_BOTTOM + 6);
        const latest = pick(slice[slice.length - 1]);
        canvas.setAttribute("aria-label", "Latest sample: " +
          NAMES.map((name, index) => `${name} ${Math.round(latest[index])}${unit}`).join(", "));
      }
    }

    function showTooltip(clientX, clientY) {
      const { slice } = visible();
      if (hover < 0 || hover >= slice.length) {
        tooltip.hidden = true;
        return;
      }
      const sample = slice[hover];
      const values = pick(sample);
      tooltip.textContent = [new Date(sample.ts).toLocaleTimeString()]
        .concat(NAMES.map((name, index) => `${name}: ${Math.round(values[index] * 10) / 10}${unit}`))
        .join("\n");
      tooltip.hidden = false;
      tooltip.style.left = `${Math.min(clientX + 12, window.innerWidth - tooltip.offsetWidth - 8)}px`;
      tooltip.style.top = `${clientY + 12}px`;
    }

    function setHover(index, clientX, clientY) {
      hover = index;
      draw();
      showTooltip(clientX, clientY);
    }

    canvas.addEventListener("mousemove", (event) => {
      const { rect, groupWidth, slice } = visible();
      const index = Math.floor((event.clientX - rect.left - PAD_LEFT) / groupWidth);
      setHover(index >= 0 && index < slice.length ? index : -1, event.clientX, event.clientY);
    });
    canvas.addEventListener("mouseleave", () => setHover(-1, 0, 0));
    canvas.addEventListener("keydown", (event) => {
      const { rect, slice } = visible();
      if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
        event.preventDefault();
        const step = event.key === "ArrowLeft" ? -1 : 1;
        const start = hover < 0 ? slice.length - 1 : hover + step;
        setHover(Math.max(0, Math.min(slice.length - 1, start)), rect.left + PAD_LEFT, rect.top + rect.height);
      } else if (event.key === "Escape") {
        setHover(-1, 0, 0);
      }
    });
    canvas.addEventListener("blur", () => setHover(-1, 0, 0));

    return { draw };
  }

  function drawAll() {
    for (const chart of charts) {
      chart.draw();
    }
  }
  window.addEventListener("resize", drawAll);

  return {
    push(snapshot) {
      samples.push(extract(snapshot));
      if (samples.length > WINDOW) {
        samples.splice(0, samples.length - WINDOW);
      }
      drawAll();
    },
    replace(snapshots) {
      samples.length = 0;
      for (const snapshot of snapshots.slice(-WINDOW)) {
        samples.push(extract(snapshot));
      }
      drawAll();
    },
  };
}
