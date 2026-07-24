const PROCESSES = ["xvfb", "openbox", "x11vnc", "novnc", "valkey", "llama", "chromium", "controller"];
const LOOKBACKS = ["15m", "1h", "3h", "12h", "1d", "3d", "7d", "30d"];
const SPANS = { "15m": 900e3, "1h": 3600e3, "3h": 10800e3, "12h": 43200e3, "1d": 86400e3, "3d": 259200e3, "7d": 604800e3, "30d": 2592000e3 };
const TICK_STEPS = {
  "15m": [300e3],
  "1h": [900e3],
  "3h": [1800e3, 3600e3],
  "12h": [7200e3, 10800e3],
  "1d": [14400e3, 21600e3],
  "3d": [43200e3, 86400e3],
  "7d": [86400e3, 172800e3],
  "30d": [604800e3, 864000e3],
};
// Finer steps (descending) used when a short data span crosses no preferred
// boundary, so the x-axis is never left without labels.
const FALLBACK_STEPS = [1800e3, 900e3, 600e3, 300e3, 120e3, 60e3, 30e3, 10e3];
const LOOKBACK_TITLES = {
  "15m": "Last 15 minutes",
  "1h": "Last hour",
  "3h": "Last 3 hours",
  "12h": "Last 12 hours",
  "1d": "Last day",
  "3d": "Last 3 days",
  "7d": "Last 7 days",
  "30d": "Last 30 days",
};
const PAD = { left: 52, right: 8, top: 8, bottom: 24 };

function boundaryTicks(firstTs, lastTs, stepMs, timezoneOffsetMinutes) {
  if (!Number.isFinite(firstTs) || !Number.isFinite(lastTs) || lastTs <= firstTs) return [];
  const offsetMs = timezoneOffsetMinutes * 60000;
  const firstLocal = firstTs - offsetMs;
  const firstBoundary = (Math.floor(firstLocal / stepMs) + 1) * stepMs + offsetMs;
  const ticks = [];
  for (let tick = firstBoundary; tick < lastTs; tick += stepMs) ticks.push(tick);
  return ticks;
}

export function chooseTicks(firstTs, lastTs, widthPx, lookback, timezoneOffsetMinutes = 0) {
  const bound = widthPx < 480 ? 3 : 5;
  const candidates = TICK_STEPS[lookback] ?? TICK_STEPS["1h"];
  let stepMs = candidates.at(-1);
  let ticks = [];
  for (const candidate of candidates) {
    const candidateTicks = boundaryTicks(firstTs, lastTs, candidate, timezoneOffsetMinutes);
    stepMs = candidate;
    ticks = candidateTicks;
    if (ticks.length <= bound) break;
  }
  if (ticks.length < 2) {
    for (const candidate of FALLBACK_STEPS) {
      if (candidate >= stepMs) continue;
      const finer = boundaryTicks(firstTs, lastTs, candidate, timezoneOffsetMinutes);
      if (finer.length >= 2) {
        return { stepMs: candidate, ticks: finer.slice(0, bound) };
      }
      if (finer.length > ticks.length) {
        stepMs = candidate;
        ticks = finer;
      }
    }
  }
  return { stepMs, ticks: ticks.slice(0, bound) };
}

// Keep segment-edge buckets at the server resolution even when a data gap follows.
export function barTimestampBounds(part, index, resSec) {
  const sample = part[index];
  const halfBucket = resSec * 500;
  return {
    leftTs: index > 0 ? (part[index - 1].ts + sample.ts) / 2 : sample.ts - halfBucket,
    rightTs: index + 1 < part.length
      ? (sample.ts + part[index + 1].ts) / 2
      : sample.ts + halfBucket,
  };
}

function metricSample(snapshot) {
  const byName = new Map((snapshot.processes ?? []).map((process) => [process.name, process]));
  return {
    ts: Number(snapshot.ts),
    procCPU: PROCESSES.map((name) => Number(byName.get(name)?.cpuPct) || 0),
    procMemMB: PROCESSES.map((name) => Number(byName.get(name)?.memMB) || 0),
    gpuUtil: Number(snapshot.system?.gpuUtil) || 0,
    gpuMemMB: Number(snapshot.system?.gpuMemMB) || 0,
  };
}

function css(name) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
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

function drawYAxes(context, width, height, max, unit, formatTick) {
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
    const value = max * line / 4;
    context.fillText(formatTick ? formatTick(value) : `${Math.round(value)}${unit}`, PAD.left - 5, y);
  }
}

// The x-domain is always the full lookback window (anchored at the newest
// sample, or now), so switching windows visibly rescales sparse data instead
// of drawing an identical data-extent chart.
function scales(samples, width, height, max, spanMs) {
  const last = samples.at(-1)?.ts ?? Date.now();
  const first = spanMs ? last - spanMs : (samples[0]?.ts ?? last - 1);
  const span = Math.max(last - first, 1);
  const plotWidth = width - PAD.left - PAD.right;
  return {
    first,
    last,
    x: (ts) => PAD.left + (ts - first) / span * plotWidth,
    invertX: (x) => first + (x - PAD.left) / Math.max(plotWidth, 1) * span,
    y: (value) => PAD.top + (1 - value / Math.max(max, 1)) * (height - PAD.top - PAD.bottom),
  };
}

function drawXAxis(context, width, height, scale, lookback) {
  const timezoneOffset = new Date((scale.first + scale.last) / 2).getTimezoneOffset();
  const { stepMs, ticks } = chooseTicks(scale.first, scale.last, width, lookback, timezoneOffset);
  const options = stepMs < 86400e3
    ? { hour: "numeric", minute: "2-digit" }
    : lookback === "30d"
      ? { month: "short", day: "numeric" }
      : { weekday: "short", day: "numeric" };
  const formatter = new Intl.DateTimeFormat(undefined, options);
  const floor = height - PAD.bottom;
  context.font = `10px ${css("--font-body")}`;
  context.fillStyle = css("--muted");
  context.strokeStyle = css("--border");
  context.textBaseline = "top";
  for (const tick of ticks) {
    const x = scale.x(tick);
    context.beginPath();
    context.moveTo(x, floor);
    context.lineTo(x, floor + 4);
    context.stroke();
    const label = formatter.format(tick);
    const halfWidth = context.measureText(label).width / 2;
    context.textAlign = x - halfWidth < PAD.left ? "start" : x + halfWidth > width - PAD.right ? "end" : "center";
    context.fillText(label, x, floor + 6);
  }
}

export function nearestSampleIndex(samples, timestamp) {
  if (samples.length === 0) return -1;
  let low = 0;
  let high = samples.length - 1;
  while (low < high) {
    const middle = Math.floor((low + high) / 2);
    if (samples[middle].ts < timestamp) low = middle + 1;
    else high = middle;
  }
  if (low === 0) return 0;
  return timestamp - samples[low - 1].ts <= samples[low].ts - timestamp ? low - 1 : low;
}

function makeLegendItem(name, index) {
  const item = document.createElement("li");
  const swatch = document.createElement("span");
  swatch.className = "swatch";
  swatch.style.background = css(`--p${index % 8 + 1}`);
  swatch.style.opacity = index >= 8 ? "0.55" : "1";
  item.append(swatch, name);
  return item;
}

function makeChart({
  canvas, legend, title, series, seriesNames, unit, maxFn, getSamples, getResolution,
  getLookback, tooltip, collapseLegend = false, renderSeries, formatValue, formatTick,
}) {
  let hover = -1;
  let expanded = false;
  const tooltipFormatter = new Intl.DateTimeFormat(undefined, { dateStyle: "short", timeStyle: "medium" });

  function updateLegend(names) {
    const visibleNames = collapseLegend && names.length > 8 && !expanded ? names.slice(0, 4) : names;
    const items = visibleNames.map((name, index) => makeLegendItem(name, index));
    if (collapseLegend && names.length > 8) {
      const more = document.createElement("li");
      more.className = "legend-more";
      const button = document.createElement("button");
      button.type = "button";
      button.setAttribute("aria-expanded", String(expanded));
      button.textContent = expanded ? "Show less" : `+${names.length - 4} more`;
      button.addEventListener("click", () => {
        expanded = !expanded;
        updateLegend(names);
      });
      more.append(button);
      items.push(more);
    }
    legend.replaceChildren(...items);
  }

  function segments(samples, resSec) {
    const result = [];
    let start = 0;
    for (let index = 1; index <= samples.length; index++) {
      if (index === samples.length || samples[index].ts - samples[index - 1].ts > resSec * 3000) {
        if (index > start) result.push(samples.slice(start, index));
        start = index;
      }
    }
    return result;
  }

  function barBounds(part, index, scale, width, resSec) {
    const { leftTs, rightTs } = barTimestampBounds(part, index, resSec);
    const left = Math.max(PAD.left, scale.x(leftTs));
    const right = Math.min(width - PAD.right, scale.x(rightTs));
    return { left, width: Math.max(right - left, 1) };
  }

  function defaultRenderer({ context, width, scale, parts, names, resSec }) {
    for (const part of parts) {
      part.forEach((sample, sampleIndex) => {
        const bar = barBounds(part, sampleIndex, scale, width, resSec);
        let base = 0;
        const values = series(sample);
        for (let index = 0; index < names.length; index++) {
          const value = values[index] || 0;
          const top = base + value;
          context.fillStyle = css(`--p${index % 8 + 1}`);
          context.globalAlpha = index >= 8 ? 0.55 : 1;
          context.fillRect(bar.left, scale.y(top), bar.width, scale.y(base) - scale.y(top));
          base = top;
        }
      });
    }
    context.globalAlpha = 1;
  }

  function draw() {
    const samples = getSamples();
    const names = seriesNames();
    const { context, width, height } = setup(canvas);
    const max = maxFn(samples, names);
    const scale = scales(samples, width, height, max, SPANS[getLookback()]);
    const parts = segments(samples, getResolution());
    drawYAxes(context, width, height, max, unit, formatTick);
    (renderSeries ?? defaultRenderer)({
      context, width, height, scale, parts, names, resSec: getResolution(), barBounds,
    });
    drawXAxis(context, width, height, scale, getLookback());
    updateLegend(names);
  }

  function show(index, clientX, clientY) {
    hover = index;
    const sample = getSamples()[index];
    if (!sample) {
      tooltip.hidden = true;
      return;
    }
    const names = seriesNames();
    // Only the five largest series at this instant; the rest collapse to one line.
    const ranked = series(sample)
      .map((value, valueIndex) => ({ value, valueIndex }))
      .sort((a, b) => b.value - a.value);
    const top = ranked.slice(0, 5).map(({ value, valueIndex }) =>
      `${names[valueIndex]}: ${formatValue ? formatValue(value, valueIndex) : value}`);
    if (ranked.length > 5) top.push(`+${ranked.length - 5} more`);
    tooltip.textContent = [title, tooltipFormatter.format(sample.ts), ...top].join("\n");
    tooltip.hidden = false;
    tooltip.style.left = `${Math.min(clientX + 12, innerWidth - tooltip.offsetWidth - 8)}px`;
    tooltip.style.top = `${Math.min(clientY + 12, innerHeight - tooltip.offsetHeight - 8)}px`;
  }

  canvas.addEventListener("mousemove", (event) => {
    const samples = getSamples();
    const rect = canvas.getBoundingClientRect();
    const scale = scales(samples, rect.width, rect.height, 1, SPANS[getLookback()]);
    const x = Math.max(PAD.left, Math.min(rect.width - PAD.right, event.clientX - rect.left));
    show(nearestSampleIndex(samples, scale.invertX(x)), event.clientX, event.clientY);
  });
  canvas.addEventListener("mouseleave", () => show(-1, 0, 0));
  canvas.addEventListener("keydown", (event) => {
    const samples = getSamples();
    if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
      event.preventDefault();
      const next = hover < 0 ? samples.length - 1 : hover + (event.key === "ArrowLeft" ? -1 : 1);
      const rect = canvas.getBoundingClientRect();
      show(Math.max(0, Math.min(samples.length - 1, next)), rect.left + PAD.left, rect.bottom);
    } else if (event.key === "Escape") {
      show(-1, 0, 0);
    }
  });

  return { draw };
}

export function initCharts(send) {
  const gpuFigure = document.querySelector("#gpu-chart");
  const tooltip = document.querySelector("#chart-tooltip");
  const lookbackBoxes = [...document.querySelectorAll("[data-lookback]")];
  let lookback = LOOKBACKS.includes(localStorage.getItem("vm-lookback")) ? localStorage.getItem("vm-lookback") : "1h";
  let samples = [];
  let resSec = 2;
  let live = false;
  let gpuEnabled;
  let gpuMemTotalMB = 0;
  let memTotalMB = 0;
  let lastDrawMs = 0;

  function request() {
    if (live) {
      send({ type: "metrics-req", lookback });
    }
  }
  function select(value) {
    if (!LOOKBACKS.includes(value)) return;
    lookback = value;
    localStorage.setItem("vm-lookback", value);
    for (const button of document.querySelectorAll("[data-lookback] button")) {
      button.setAttribute("aria-checked", String(button.dataset.value === value));
      button.tabIndex = button.dataset.value === value ? 0 : -1;
    }
    request();
  }
  for (const box of lookbackBoxes) {
    const label = document.createElement("span");
    label.className = "lookback-label";
    label.textContent = "Window";
    box.append(label);
    for (const value of LOOKBACKS) {
      const button = document.createElement("button");
      button.type = "button";
      button.role = "radio";
      button.dataset.value = value;
      button.textContent = value;
      button.title = LOOKBACK_TITLES[value];
      button.setAttribute("aria-label", `Time window: ${LOOKBACK_TITLES[value].toLowerCase()}`);
      button.addEventListener("click", () => select(value));
      button.addEventListener("keydown", (event) => {
        if (!["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown"].includes(event.key)) return;
        event.preventDefault();
        const step = event.key === "ArrowLeft" || event.key === "ArrowUp" ? -1 : 1;
        const next = (LOOKBACKS.indexOf(lookback) + step + LOOKBACKS.length) % LOOKBACKS.length;
        select(LOOKBACKS[next]);
        box.querySelector(`[data-value="${LOOKBACKS[next]}"]`).focus();
      });
      box.append(button);
    }
  }
  select(lookback);

  const shared = {
    getSamples: () => samples,
    getResolution: () => resSec,
    getLookback: () => lookback,
    tooltip,
  };
  const cpuChart = makeChart({
    ...shared,
    canvas: document.querySelector("#chart-cpu"),
    legend: document.querySelector("#core-legend"),
    title: "CPU load",
    series: (sample) => sample.procCPU,
    seriesNames: () => PROCESSES,
    unit: "%",
    maxFn: (chartSamples) => {
      const peak = Math.max(100, ...chartSamples.map((sample) =>
        sample.procCPU.reduce((sum, value) => sum + (value || 0), 0)));
      return Math.ceil(peak / 100) * 100;
    },
    formatValue: (value) => `${value.toFixed(1)}%`,
  });
  const memoryChart = makeChart({
    ...shared,
    canvas: document.querySelector("#chart-mem"),
    legend: document.querySelector("#process-legend"),
    title: "Memory",
    series: (sample) => sample.procMemMB,
    seriesNames: () => PROCESSES,
    unit: "MiB",
    // Fixed 0 → system total so process memory reads against real capacity.
    maxFn: (chartSamples) => Math.max(
      memTotalMB,
      1,
      ...chartSamples.map((sample) => sample.procMemMB.reduce((sum, value) => sum + (value || 0), 0)),
    ),
    formatTick: (value) => value >= 1024
      ? `${(value / 1024).toFixed(value % 1024 === 0 ? 0 : 1)}GiB`
      : `${Math.round(value)}MiB`,
    formatValue: (value) => `${value} MiB`,
  });
  const gpuChart = makeChart({
    ...shared,
    canvas: document.querySelector("#chart-gpu"),
    legend: document.querySelector("#gpu-legend"),
    title: "GPU utilization",
    series: (sample) => [sample.gpuUtil, sample.gpuMemMB],
    seriesNames: () => ["utilization", "memory used"],
    unit: "%",
    maxFn: () => 100,
    formatValue: (value, index) => index === 0 ? `${value.toFixed(1)}%` : `${value.toFixed(1)} MiB`,
    renderSeries({ context, width, scale, parts, resSec: resolution, barBounds }) {
      const memoryMax = Math.max(gpuMemTotalMB, 1, ...samples.map((sample) => sample.gpuMemMB || 0));
      const memoryScale = scales(samples, width, context.canvas.getBoundingClientRect().height, memoryMax, SPANS[lookback]);
      context.fillStyle = css("--p1");
      for (const part of parts) {
        part.forEach((sample, index) => {
          const bar = barBounds(part, index, scale, width, resolution);
          context.fillRect(bar.left, scale.y(sample.gpuUtil || 0), bar.width, scale.y(0) - scale.y(sample.gpuUtil || 0));
        });
        context.beginPath();
        part.forEach((sample, index) => {
          const x = memoryScale.x(sample.ts);
          const y = memoryScale.y(sample.gpuMemMB || 0);
          if (index === 0) context.moveTo(x, y);
          else context.lineTo(x, y);
        });
        context.strokeStyle = css("--p2");
        context.lineWidth = 2;
        context.stroke();
      }
      context.fillStyle = css("--muted");
      context.font = `10px ${css("--font-body")}`;
      context.textAlign = "right";
      context.textBaseline = "top";
      context.fillText(`${Math.round(memoryMax)} MiB`, width - PAD.right, PAD.top);
    },
  });

  function draw() {
    lastDrawMs = Date.now();
    cpuChart.draw();
    memoryChart.draw();
    if (gpuEnabled) gpuChart.draw();
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
        ts: Number(sample.ts),
        procCPU: (sample.procCPU ?? []).map(Number),
        procMemMB: (sample.procMemMB ?? []).map(Number),
        gpuUtil: Number(sample.gpuUtil) || 0,
        gpuMemMB: Number(sample.gpuMemMB) || 0,
      }));
      memTotalMB = Number(message.samples?.at(-1)?.memTotalMB) || memTotalMB;
      draw();
    },
    push(snapshot) {
      let configured = false;
      if (gpuEnabled === undefined) {
        gpuEnabled = Boolean(snapshot.gpu?.sampler);
        gpuFigure.hidden = !gpuEnabled;
        configured = true;
      }
      if (gpuEnabled) {
        gpuMemTotalMB = Number(snapshot.system?.gpuMemTotalMB) || gpuMemTotalMB;
      }
      memTotalMB = Number(snapshot.system?.memTotalMB) || memTotalMB;
      if (!["15m", "1h"].includes(lookback)) {
        if (configured) draw();
        return;
      }
      samples.push(metricSample(snapshot));
      const cutoff = Date.now() - SPANS[lookback];
      while (samples[0]?.ts < cutoff) samples.shift();
      resSec = 2;
      // Samples accumulate at the 2 s state cadence, but the charts repaint
      // at most once per 15 s to keep the page calm.
      if (configured || Date.now() - lastDrawMs >= 15000) draw();
    },
    draw,
  };
}
