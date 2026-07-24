const shortTime = new Intl.DateTimeFormat([], { hour: "numeric", minute: "2-digit", second: "2-digit" });
const fullTime = new Intl.DateTimeFormat([], { dateStyle: "medium", timeStyle: "medium" });

/** @typedef {Record<string, any>} Data */

/** @param {number} milliseconds */
export function formatDuration(milliseconds) {
  const value = Math.max(0, Number(milliseconds) || 0);
  if (value < 1000) return `${Math.round(value)} ms`;
  if (value < 60000) return `${(value / 1000).toFixed(1)} s`;
  const minutes = Math.floor(value / 60000);
  return `${minutes}m ${Math.floor((value % 60000) / 1000)}s`;
}

/** @param {Data} job */
export function queueSummary(job) {
  const payload = job?.payload ?? {};
  switch (job?.type) {
    case "chat":
      return String(payload.text ?? "Chat generation");
    case "project-run":
      return String(payload.name ?? `Project ${job.projectId ?? payload.id ?? ""}`).trim();
    case "manual-tool":
      return String(payload.tool ?? payload.name ?? "Manual tool");
    case "soak-probe":
      return String(payload.echo ?? "Queue probe");
    default:
      return String(job?.type ?? "Job");
  }
}

/** Short type-derived primary label for a queue row. @param {Data} job */
export function jobName(job) {
  const payload = job?.payload ?? {};
  switch (job?.type) {
    case "chat":
      return "Chat";
    case "project-run":
      return `Project: ${String(payload.name ?? job.projectId ?? payload.id ?? "?").trim()}`;
    case "manual-tool":
      return `Tool: ${String(payload.tool ?? payload.name ?? "?")}`;
    case "soak-probe":
      return "Queue probe";
    default:
      return String(job?.type ?? "Job");
  }
}

/** Truncated secondary summary shown after the primary label. @param {Data} job */
export function jobSecondary(job) {
  const payload = job?.payload ?? {};
  switch (job?.type) {
    case "chat":
      return String(payload.text ?? "");
    case "project-run":
      return job.selector ? `selector ${job.selector}` : "manual run";
    case "manual-tool":
      return payload.args ? JSON.stringify(payload.args) : "";
    case "soak-probe":
      return String(payload.echo ?? "");
    default:
      return "";
  }
}

/** @param {string} name */
function icon(name) {
  const namespace = "http:" + "//www.w3.org/2000/svg";
  const svg = document.createElementNS(namespace, "svg");
  svg.setAttribute("class", "icon");
  svg.setAttribute("aria-hidden", "true");
  const use = document.createElementNS(namespace, "use");
  use.setAttribute("href", `/icons.svg#i-${name}`);
  svg.append(use);
  return svg;
}

/** @param {number} ts @param {Intl.DateTimeFormat} [formatter] */
function time(ts, formatter = shortTime) {
  return ts ? formatter.format(new Date(Number(ts))) : "…";
}

/** @param {string} type */
function chip(type) {
  const span = document.createElement("span");
  span.className = `job-chip type-${String(type).replaceAll(/[^a-z0-9-]/gi, "-")}`;
  span.textContent = type || "unknown";
  return span;
}

/** @param {Element} parent @param {string} className @param {string} text */
function appendText(parent, className, text) {
  const span = document.createElement("span");
  span.className = className;
  span.textContent = text;
  parent.append(span);
  return span;
}

/** @param {Array<[string, any]>} rows */
function definitionList(rows) {
  const list = document.createElement("dl");
  list.className = "job-detail-list";
  for (const [term, value] of rows) {
    const row = document.createElement("div");
    const dt = document.createElement("dt");
    const dd = document.createElement("dd");
    dt.textContent = term;
    if (value instanceof Node) dd.append(value);
    else dd.textContent = String(value ?? "…");
    row.append(dt, dd);
    list.append(row);
  }
  return list;
}

/** @param {any} value */
function pre(value) {
  const element = document.createElement("pre");
  element.textContent = typeof value === "string" ? value : JSON.stringify(value ?? {}, null, 2);
  return element;
}

/** @param {(value: Data) => void} send */
export function initJobs(send) {
  const upcoming = /** @type {HTMLOListElement} */ (document.querySelector("#queue-upcoming"));
  const running = /** @type {HTMLOListElement} */ (document.querySelector("#queue-running"));
  const finished = /** @type {HTMLOListElement} */ (document.querySelector("#queue-finished"));
  const queueEmpty = /** @type {HTMLParagraphElement} */ (document.querySelector("#queue-empty"));
  const activityList = /** @type {HTMLOListElement} */ (document.querySelector("#activity-list"));
  const activityEmpty = /** @type {HTMLParagraphElement} */ (document.querySelector("#activity-empty"));
  const detail = /** @type {HTMLElement} */ (document.querySelector("#job-detail"));
  const curtain = /** @type {HTMLElement} */ (document.querySelector("#job-detail-curtain"));
  /** @type {Data} */
  let queue = { upcoming: [], running: null, finished: [] };
  /** @type {Data[]} */
  let activities = [];
  /** @type {{kind: string, value: Data} | undefined} */
  let selected;
  /** @type {HTMLElement | undefined} */
  let previousFocus;

  function closeDetail() {
    if (detail.hidden) return;
    detail.classList.remove("open");
    curtain.classList.remove("open");
    document.body.classList.remove("job-detail-locked");
    curtain.hidden = true;
    detail.hidden = true;
    previousFocus?.focus();
  }

  /** @param {string} type @param {number} ts */
  function header(type, ts) {
    const element = document.createElement("header");
    const meta = document.createElement("div");
    meta.append(chip(type));
    appendText(meta, "job-detail-time", time(ts, fullTime));
    const close = document.createElement("button");
    close.type = "button";
    close.className = "icon-button job-detail-close";
    close.setAttribute("aria-label", "Close details");
    close.append(icon("x"));
    close.addEventListener("click", closeDetail);
    element.append(meta, close);
    return element;
  }

  /** @param {Data} job */
  function queueDetails(job) {
    const fragment = document.createDocumentFragment();
    const payload = job.payload ?? {};
    fragment.append(header(job.type, job.result?.finishedTs ?? job.startedTs ?? job.enqueuedTs));
    const title = document.createElement("h2");
    title.textContent = queueSummary(job);
    fragment.append(title);
    if (job.type === "chat") {
      const prompt = document.createElement("p");
      prompt.className = "job-detail-prompt";
      prompt.textContent = String(payload.text ?? "");
      fragment.append(prompt);
      fragment.append(definitionList([
        ["Enqueued", time(job.enqueuedTs, fullTime)],
        ["Not before", time(job.notBeforeTs, fullTime)],
        ["Started", time(job.startedTs, fullTime)],
        ["Finished", time(job.result?.finishedTs, fullTime)],
        ["Attempts", job.attempts ?? 0],
        ["Initiator", job.initiatorConn || "scheduled"],
        ["Result", job.result?.summary || job.lastError || "Pending"],
      ]));
    } else if (job.type === "project-run") {
      const projectID = job.projectId || payload.id;
      const link = document.createElement("a");
      link.href = `/projects/${encodeURIComponent(projectID ?? "")}`;
      link.dataset.nav = "";
      link.textContent = queueSummary(job);
      const grounding = job.selector
        ? `Scheduled by selector: ${job.selector}`
        : `Project ${projectID ?? "unknown"} manual run`;
      fragment.append(definitionList([
        ["Project", link],
        ["Grounding", grounding],
        ["Result", job.result?.summary || job.lastError || "Pending"],
        ["Duration", job.result ? formatDuration(job.result.finishedTs - (job.startedTs || job.enqueuedTs)) : "…"],
      ]));
    } else {
      const heading = document.createElement("h3");
      heading.textContent = "Payload";
      fragment.append(heading, pre(payload), definitionList([
        ["Result", job.result?.summary || job.lastError || "Pending"],
        ["Duration", job.result ? formatDuration(job.result.finishedTs - (job.startedTs || job.enqueuedTs)) : "…"],
      ]));
    }
    return fragment;
  }

  /** @param {Data} event */
  function activityDetails(event) {
    const fragment = document.createDocumentFragment();
    const data = event.detail ?? {};
    fragment.append(header(event.kind, event.ts));
    const title = document.createElement("h2");
    title.textContent = event.name || event.kind;
    const summary = document.createElement("p");
    summary.textContent = event.summary || "";
    fragment.append(title, summary);
    if (event.kind === "tool") {
      const argsTitle = document.createElement("h3");
      argsTitle.textContent = "Arguments";
      const resultTitle = document.createElement("h3");
      resultTitle.textContent = "Result";
      fragment.append(argsTitle, pre(data.args), resultTitle, pre(data.resultText || ""));
      if (data.screenshotThumb) {
        const image = document.createElement("img");
        image.src = data.screenshotThumb;
        image.alt = `${event.name} step screenshot`;
        fragment.append(image);
      }
      fragment.append(definitionList([
        ["Status", data.ok ? "OK" : "Error"],
        ["Duration", formatDuration(data.durationMs)],
        ["Job", event.jobId || "…"],
      ]));
    } else if (event.kind === "llm") {
      fragment.append(definitionList([
        ["Phase", data.phase || "…"],
        ["Prompt", event.summary],
        ["Prompt tokens", data.promptTokens ?? 0],
        ["Completion tokens", data.completionTokens ?? 0],
        ["Duration", formatDuration(data.durationMs)],
        ["Stopped", data.stopped ? "Yes" : "No"],
        ["Job", event.jobId || "…"],
      ]));
    } else {
      fragment.append(definitionList([
        ["Status", data.ok ? "OK" : "Error"],
        ["Characters", data.chars || "…"],
        ["Voice", data.voice || "…"],
        ["Recipient domain", data.recipientDomain || "…"],
        ["Size", data.size ? `${data.size} bytes` : "…"],
        ["Duration", formatDuration(data.durationMs)],
      ]));
    }
    return fragment;
  }

  /** @param {string} kind @param {Data} value @param {HTMLElement} trigger */
  function openDetail(kind, value, trigger) {
    selected = { kind, value };
    previousFocus = trigger ?? document.activeElement;
    detail.replaceChildren(kind === "queue" ? queueDetails(value) : activityDetails(value));
    detail.hidden = false;
    if (matchMedia("(max-width: 47.999rem)").matches) {
      curtain.hidden = false;
      document.body.classList.add("job-detail-locked");
    }
    requestAnimationFrame(() => {
      detail.classList.add("open");
      curtain.classList.add("open");
      /** @type {HTMLElement | null} */ (detail.querySelector(".job-detail-close"))?.focus();
    });
  }

  /** @param {Data} job @param {string} phase */
  function queueRow(job, phase) {
    const item = document.createElement("li");
    const button = document.createElement("button");
    button.type = "button";
    button.className = `job-row ${phase}`;
    if (phase === "finished") {
      // Single small status dot; no clock icon, so success is one light per row.
      const dot = document.createElement("span");
      dot.className = `job-result-dot ${job.result?.ok ? "ok" : "error"}`;
      dot.setAttribute("aria-label", job.result?.ok ? "Succeeded" : "Failed");
      button.append(dot);
    } else {
      button.append(icon("clock-3"));
    }
    button.append(chip(job.type));
    appendText(button, "job-row-name", jobName(job));
    appendText(button, "job-row-summary", jobSecondary(job));
    const meta = appendText(button, "job-row-meta", "");
    if (phase === "upcoming") {
      meta.textContent = Number(job.notBeforeTs) > Date.now()
        ? `not before ${time(job.notBeforeTs)}`
        : `queued ${time(job.enqueuedTs)}`;
    } else if (phase === "running") {
      const update = () => {
        meta.textContent = `running ${formatDuration(Date.now() - Number(job.startedTs || job.enqueuedTs))}`;
      };
      update();
      button.dataset.elapsed = "";
    } else {
      const duration = Number(job.result?.finishedTs) - Number(job.startedTs || job.enqueuedTs);
      meta.textContent = `${time(job.result?.finishedTs)} · ${formatDuration(duration)}`;
    }
    button.addEventListener("click", () => openDetail("queue", job, button));
    item.append(button);
    return item;
  }

  /** @param {HTMLOListElement} list @param {Data[]} jobs @param {string} phase */
  function renderGroup(list, jobs, phase) {
    list.replaceChildren(...jobs.map((job) => queueRow(job, phase)));
    const group = list.closest(".queue-group");
    if (group instanceof HTMLElement) group.hidden = jobs.length === 0;
  }

  function renderQueue() {
    const next = /** @type {Data[]} */ ((queue.upcoming ?? []).slice(0, 10));
    const current = /** @type {Data[]} */ (queue.running ? [queue.running] : []);
    const recent = /** @type {Data[]} */ ((queue.finished ?? []).slice(0, 10));
    renderGroup(upcoming, next, "upcoming");
    renderGroup(running, current, "running");
    renderGroup(finished, recent, "finished");
    queueEmpty.hidden = next.length + current.length + recent.length > 0;
    if (selected?.kind === "queue") {
      const selectedID = selected.value.id;
      const match = [...next, ...current, ...recent].find((job) => job.id === selectedID);
      if (match) selected.value = match;
    }
  }

  /** @param {Data} event */
  function activityRow(event) {
    const item = document.createElement("li");
    const button = document.createElement("button");
    button.type = "button";
    button.className = "activity-row";
    appendText(button, "activity-time", time(event.ts));
    button.append(chip(event.kind || "event"));
    appendText(button, "activity-name", event.name || event.kind);
    appendText(button, "activity-summary", event.summary || "");
    button.addEventListener("click", () => openDetail("activity", event, button));
    item.append(button);
    return item;
  }

  function renderActivity() {
    activities = activities.slice(0, 200);
    activityList.replaceChildren(...activities.map(activityRow));
    activityEmpty.hidden = activities.length > 0;
  }

  curtain.addEventListener("click", closeDetail);
  detail.addEventListener("keydown", (event) => {
    if (event.key === "Escape") {
      event.preventDefault();
      closeDetail();
    } else if (event.key === "Tab" && detail.classList.contains("open")) {
      const controls = /** @type {HTMLElement[]} */ ([...detail.querySelectorAll("a[href], button:not([disabled])")]);
      const first = controls[0];
      const last = controls.at(-1);
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last?.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first?.focus();
      }
    }
  });

  setInterval(() => {
    const row = running.querySelector("[data-elapsed]");
    if (row && queue.running) {
      /** @type {HTMLElement} */ (row.querySelector(".job-row-meta")).textContent =
        `running ${formatDuration(Date.now() - Number(queue.running.startedTs || queue.running.enqueuedTs))}`;
    }
  }, 1000);

  return {
    /** @param {Data} message */
    frame(message) {
      if (message.type === "queue-state") {
        queue = message;
        renderQueue();
      } else if (message.type === "activity") {
        activities = Array.isArray(message.events) ? message.events : [];
        renderActivity();
      } else if (message.type === "activity-event" && message.event) {
        activities.unshift(message.event);
        renderActivity();
      }
    },
    enter() {
      send({ type: "activity-req" });
      send({ type: "queue-peek" });
    },
    close: closeDetail,
  };
}
