const dayOptions = [
  ["everyday", "Every day"], ["weekday", "Weekdays"], ["weekend", "Weekend"],
  ["mon", "Mon"], ["tue", "Tue"], ["wed", "Wed"], ["thu", "Thu"],
  ["fri", "Fri"], ["sat", "Sat"], ["sun", "Sun"],
];
const timeOptions = [
  ["anytime", "Any time"], ["early-morning", "Early morning"], ["morning", "Morning"],
  ["afternoon", "Afternoon"], ["evening", "Evening"], ["night", "Night"],
  ["late-night", "Late night"],
];
const individualDays = new Set(["mon", "tue", "wed", "thu", "fri", "sat", "sun"]);
const relative = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });
const dateTime = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });
const svgNamespace = "http:" + "//www.w3.org/2000/svg";

/**
 * @typedef {{id:string,name:string,task:string,selector:string,enabled:boolean,createdTs:number,lastRunTs:number,lastEnqueuedBucket:string}} Project
 * @typedef {{ts:number,jobId:string,ok:boolean,summary:string,durationMs:number,manual:boolean}} ProjectRun
 * @typedef {{type:string,projects?:Project[],runs?:Record<string,ProjectRun[]>,running?:{type?:string,projectId?:string}|null,upcoming?:Array<{type?:string,projectId?:string}>}} ProjectFrame
 * @typedef {(value:Record<string, unknown>) => boolean} Send
 */

/** @param {string} selector @returns {{days:Set<string>,time:string}} */
function selectorParts(selector) {
  const parts = selector.trim().split(/\s+/);
  const last = parts.at(-1);
  const knownTime = timeOptions.some(([value]) => value === last);
  const dayText = knownTime && parts.length > 1 ? parts[0] : (knownTime ? "everyday" : parts[0]);
  return {
    days: new Set(dayText.includes(",") || individualDays.has(dayText) ? dayText.split(",") : [dayText || "everyday"]),
    time: knownTime ? (last ?? "anytime") : "anytime",
  };
}

/** @param {Set<string>} days @param {string} time */
export function serializeSelector(days, time) {
  let dayText = [...days][0] ?? "everyday";
  if ([...days].every((day) => individualDays.has(day))) {
    dayText = dayOptions.filter(([value]) => individualDays.has(value) && days.has(value)).map(([value]) => value).join(",");
  }
  if (time === "anytime") {
    return dayText === "everyday" ? "anytime" : dayText;
  }
  return dayText === "everyday" ? time : `${dayText} ${time}`;
}

/** @param {string} value */
function capitalize(value) {
  return value.charAt(0).toUpperCase() + value.slice(1).replaceAll("-", " ");
}

/** @param {string} selector */
export function selectorLabel(selector) {
  const { days, time } = selectorParts(selector);
  const only = [...days][0] ?? "everyday";
  let dayLabel;
  if (days.size > 1 || individualDays.has(only)) {
    dayLabel = dayOptions.filter(([value]) => individualDays.has(value) && days.has(value)).map(([, label]) => label).join(", ");
  } else {
    dayLabel = only === "weekday" ? "Every weekday" : only === "weekend" ? "Every weekend day" : "Every day";
  }
  if (time === "anytime") {
    return `${dayLabel}${days.size > 1 || individualDays.has(only) ? ", any time" : ""}`;
  }
  if (only === "fri" && days.size === 1) {
    return `Fridays at ${time.replaceAll("-", " ")}`;
  }
  return `${dayLabel} ${time.replaceAll("-", " ")}`;
}

/** @param {number} timestamp */
function relativeTime(timestamp) {
  const seconds = Math.round((timestamp - Date.now()) / 1000);
  /** @type {Array<[Intl.RelativeTimeFormatUnit, number]>} */
  const units = [
    ["year", 31536000], ["month", 2592000], ["week", 604800],
    ["day", 86400], ["hour", 3600], ["minute", 60], ["second", 1],
  ];
  for (const [unit, size] of units) {
    if (Math.abs(seconds) >= size || unit === "second") {
      return relative.format(Math.round(seconds / size), unit);
    }
  }
  return "now";
}

/** @param {string} name */
function icon(name) {
  const svg = document.createElementNS(svgNamespace, "svg");
  svg.classList.add("icon");
  const use = document.createElementNS(svgNamespace, "use");
  use.setAttribute("href", `/icons.svg#i-${name}`);
  svg.append(use);
  return svg;
}

/** @param {string} path */
function navigate(path) {
  history.pushState(null, "", path);
  dispatchEvent(new PopStateEvent("popstate"));
}

/** @param {Send} send */
export function initProjects(send) {
  const list = /** @type {HTMLUListElement} */ (document.querySelector("#project-list"));
  const empty = /** @type {HTMLParagraphElement} */ (document.querySelector("#projects-empty"));
  const dialog = /** @type {HTMLDialogElement} */ (document.querySelector("#project-dialog"));
  const form = /** @type {HTMLFormElement} */ (document.querySelector("#project-create-form"));
  const nameInput = /** @type {HTMLInputElement} */ (document.querySelector("#project-name"));
  const detail = /** @type {HTMLDivElement} */ (document.querySelector("#project-detail"));
  const missing = /** @type {HTMLParagraphElement} */ (document.querySelector("#project-detail-missing"));
  const errorNotes = document.querySelectorAll(".project-error");
  /** @param {string} text */
  const showError = (text) => {
    for (const note of errorNotes) note.textContent = text;
  };
  /** @type {Project[]} */
  let projects = [];
  /** @type {Record<string, ProjectRun[]>} */
  let runs = {};
  let createBaseline = new Set();
  let pendingName = "";
  let queuedProjects = new Set();
  /** @type {ReturnType<typeof setTimeout>|undefined} */
  let deleteTimer;

  /** @param {string} id @param {Record<string, unknown>} values */
  const sendUpdate = (id, values) => send({ type: "project-update", id, ...values });

  function renderList() {
    list.replaceChildren();
    empty.hidden = projects.length !== 0;
    for (const project of projects) {
      const item = document.createElement("li");
      const link = document.createElement("a");
      link.href = `/projects/${encodeURIComponent(project.id)}`;
      link.dataset.nav = "";
      link.className = "project-row";
      const copy = document.createElement("span");
      copy.className = "project-row-copy";
      const title = document.createElement("strong");
      title.textContent = project.name;
      const summary = document.createElement("span");
      summary.textContent = selectorLabel(project.selector) + (project.lastRunTs > 0 ? ` · last ran ${relativeTime(project.lastRunTs)}` : "");
      copy.append(title, summary);
      const status = document.createElement("span");
      status.className = `project-status ${project.enabled ? "enabled" : "paused"}`;
      const dot = document.createElement("span");
      dot.className = "status-dot";
      status.append(dot, document.createTextNode(project.enabled ? "Enabled" : "Paused"));
      link.append(copy, status, icon("chevron-right"));
      item.append(link);
      list.append(item);
    }
  }

  /**
   * @param {string} label
   * @param {string[][]} options
   * @param {Set<string>|string} selected
   * @param {(value:string) => void} onSelect
   */
  function chipRow(label, options, selected, onSelect) {
    const row = document.createElement("div");
    row.className = "schedule-row";
    const heading = document.createElement("strong");
    heading.textContent = label;
    const chips = document.createElement("div");
    chips.className = "schedule-chips";
    for (const [value, text] of options) {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "schedule-chip";
      button.dataset.value = value;
      button.setAttribute("aria-pressed", String(selected instanceof Set ? selected.has(value) : selected === value));
      button.textContent = text;
      button.addEventListener("click", () => onSelect(value));
      chips.append(button);
    }
    row.append(heading, chips);
    return row;
  }

  function renderDetail() {
    if (!location.pathname.startsWith("/projects/")) return;
    const id = decodeURIComponent(location.pathname.slice("/projects/".length));
    const project = projects.find((entry) => entry.id === id);
    detail.replaceChildren();
    missing.hidden = Boolean(project);
    if (!project) return;

    const heading = document.createElement("div");
    heading.className = "project-detail-heading";
    const title = document.createElement("input");
    title.id = "project-detail-title";
    title.className = "project-title-input";
    title.value = project.name;
    title.maxLength = 80;
    title.readOnly = true;
    title.setAttribute("aria-label", "Project name");
    title.addEventListener("click", () => {
      title.readOnly = false;
      title.select();
    });
    title.addEventListener("blur", () => {
      title.readOnly = true;
      const name = title.value.trim();
      if (name && name !== project.name) sendUpdate(id, { name });
      else title.value = project.name;
    });
    title.addEventListener("keydown", (event) => {
      if (event.key === "Enter") title.blur();
      if (event.key === "Escape") {
        title.value = project.name;
        title.blur();
      }
    });
    const actions = document.createElement("div");
    actions.className = "project-heading-actions";
    const run = document.createElement("button");
    run.type = "button";
    run.append(icon("play"), document.createTextNode("Run now"));
    const queued = queuedProjects.has(id);
    run.disabled = queued;
    run.title = queued ? "queued — will run when the current job finishes" : "Runs while this page stays open; otherwise schedule it";
    run.addEventListener("click", () => {
      send({ type: "project-run", id });
      queuedProjects.add(id);
      renderDetail();
    });
    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "secondary";
    remove.append(icon("trash-2"), document.createTextNode("Delete"));
    let armed = false;
    remove.addEventListener("click", () => {
      if (!armed) {
        armed = true;
        remove.textContent = "Confirm delete";
        if (deleteTimer !== undefined) clearTimeout(deleteTimer);
        deleteTimer = setTimeout(() => {
          armed = false;
          renderDetail();
        }, 4000);
        return;
      }
      send({ type: "project-delete", id });
      navigate("/projects");
    });
    actions.append(run, remove);
    heading.append(title, actions);

    const taskCard = document.createElement("article");
    taskCard.className = "project-card";
    const taskTitle = document.createElement("h2");
    taskTitle.textContent = "Task";
    const task = document.createElement("textarea");
    task.rows = 8;
    task.maxLength = 4096;
    task.value = project.task;
    const saveTask = () => {
      if (task.value !== project.task) sendUpdate(id, { task: task.value });
    };
    task.addEventListener("blur", saveTask);
    task.addEventListener("keydown", (event) => {
      if (event.key === "Enter" && (event.ctrlKey || event.metaKey)) {
        event.preventDefault();
        saveTask();
        task.blur();
      }
    });
    const taskCaption = document.createElement("p");
    taskCaption.className = "project-caption";
    taskCaption.textContent = "Written in plain language; Virtual Me's agent follows it with the browser, bash, and mail tools.";
    taskCard.append(taskTitle, task, taskCaption);

    const scheduleCard = document.createElement("article");
    scheduleCard.className = "project-card";
    const scheduleTitle = document.createElement("h2");
    scheduleTitle.textContent = "Schedule";
    const selected = selectorParts(project.selector);
    const commitSchedule = () => sendUpdate(id, { selector: serializeSelector(selected.days, selected.time) });
    const daysRow = chipRow("Days", dayOptions, selected.days, (value) => {
      if (!individualDays.has(value)) {
        selected.days = new Set([value]);
      } else {
        if (![...selected.days].every((day) => individualDays.has(day))) selected.days.clear();
        if (selected.days.has(value) && selected.days.size > 1) selected.days.delete(value);
        else selected.days.add(value);
      }
      commitSchedule();
    });
    const timesRow = chipRow("Time", timeOptions, selected.time, (value) => {
      selected.time = value;
      commitSchedule();
    });
    const serialized = document.createElement("p");
    serialized.className = "selector-preview";
    serialized.textContent = `${selectorLabel(project.selector)} · ${project.selector}`;
    const switchRow = document.createElement("div");
    switchRow.className = "project-switch-row";
    const switchLabel = document.createElement("span");
    switchLabel.textContent = "Enabled";
    const toggle = document.createElement("button");
    toggle.type = "button";
    toggle.className = "switch";
    toggle.setAttribute("role", "switch");
    toggle.setAttribute("aria-checked", String(project.enabled));
    toggle.setAttribute("aria-label", "Enable scheduled runs");
    const knob = document.createElement("span");
    knob.className = "knob";
    toggle.append(knob);
    toggle.addEventListener("click", () => sendUpdate(id, { enabled: !project.enabled }));
    switchRow.append(switchLabel, toggle);
    scheduleCard.append(scheduleTitle, daysRow, timesRow, serialized, switchRow);

    const runsCard = document.createElement("article");
    runsCard.className = "project-card";
    const runsTitle = document.createElement("h2");
    runsTitle.textContent = "Recent runs";
    const runList = document.createElement("ul");
    runList.className = "project-runs";
    const projectRuns = runs[id] ?? [];
    if (projectRuns.length === 0) {
      const none = document.createElement("li");
      none.className = "empty-note";
      none.textContent = "No runs yet.";
      runList.append(none);
    }
    for (const item of projectRuns) {
      const row = document.createElement("li");
      const when = document.createElement("time");
      when.dateTime = new Date(item.ts).toISOString();
      when.textContent = dateTime.format(item.ts);
      const outcome = document.createElement("span");
      outcome.className = item.ok ? "run-ok" : "run-error";
      outcome.append(Object.assign(document.createElement("span"), { className: "status-dot" }), document.createTextNode(item.ok ? "Completed" : "Failed"));
      const summary = document.createElement("span");
      summary.className = "run-summary";
      summary.textContent = item.summary || "No summary";
      const duration = document.createElement("span");
      duration.textContent = `${(item.durationMs / 1000).toFixed(1)} s`;
      row.append(when, outcome, summary, duration);
      runList.append(row);
    }
    runsCard.append(runsTitle, runList);
    const data = document.createElement("p");
    data.className = "project-data";
    data.textContent = `Scratch directory: ~/.virtualme/projects/${project.id}/`;
    detail.append(heading, taskCard, scheduleCard, runsCard, data);
  }

  const newButton = /** @type {HTMLButtonElement} */ (document.querySelector("#project-new"));
  const cancelButton = /** @type {HTMLButtonElement} */ (document.querySelector("#project-cancel"));
  newButton.addEventListener("click", () => {
    form.reset();
    dialog.showModal();
    nameInput.focus();
  });
  cancelButton.addEventListener("click", () => dialog.close());
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    pendingName = nameInput.value.trim();
    if (!pendingName) return;
    createBaseline = new Set(projects.map((project) => project.id));
    send({ type: "project-create", name: pendingName });
    dialog.close();
  });

  return {
    /** @param {string} page */
    render(page) {
      if (page === "projects") renderList();
      if (page === "project-detail") renderDetail();
    },
    /** @param {ProjectFrame} message */
    frame(message) {
      showError("");
      projects = [...(message.projects ?? [])].sort((left, right) => left.name.localeCompare(right.name));
      runs = message.runs ?? {};
      renderList();
      renderDetail();
      if (pendingName) {
        const created = projects.find((project) => project.name === pendingName && !createBaseline.has(project.id));
        if (created) {
          pendingName = "";
          navigate(`/projects/${encodeURIComponent(created.id)}`);
        }
      }
    },
    /** @param {ProjectFrame} message */
    queue(message) {
      const queued = new Set();
      if (message.running?.type === "project-run") queued.add(message.running.projectId);
      for (const job of message.upcoming ?? []) {
        if (job.type === "project-run") queued.add(job.projectId);
      }
      queuedProjects = queued;
      renderDetail();
    },
    /** @param {string} text */
    error(text) {
      pendingName = "";
      showError(capitalize(text));
    },
  };
}
