export function initMail(send) {
  const form = document.querySelector("#mail-form");
  const to = document.querySelector("#mail-to");
  const subject = document.querySelector("#mail-subject");
  const body = document.querySelector("#mail-body");
  const image = document.querySelector("#mail-image");
  const submit = document.querySelector("#mail-send");
  const gmail = document.querySelector("#mail-gmail-test");
  const result = document.querySelector("#mail-result");
  let live = false;
  let active = null;

  function enabled() {
    const busy = active !== null;
    submit.disabled = !live || busy;
    gmail.disabled = !live || busy;
    for (const field of [to, subject, body, image]) field.disabled = !live || busy;
  }

  function dispatch() {
    if (!live || active !== null || !form.reportValidity()) return;
    active = crypto.randomUUID();
    result.className = "mail-result";
    result.textContent = "submitting…";
    if (!send({
      type: "mail-send", id: active, to: to.value.trim(),
      subject: subject.value, body: body.value, includeTestImage: image.checked,
    })) {
      active = null;
      result.textContent = "connection unavailable";
    }
    enabled();
  }

  form.addEventListener("submit", (event) => {
    event.preventDefault();
    dispatch();
  });
  gmail.addEventListener("click", () => {
    subject.value = "Virtual Me test message";
    body.value = "Hello from Virtual Me.\n\nThis message verifies outbound mail and its inline test image.";
    image.checked = true;
    dispatch();
  });
  for (const button of document.querySelectorAll("[data-copy]")) {
    button.addEventListener("click", async () => {
      const field = document.querySelector(`#${button.dataset.copy}`);
      await navigator.clipboard.writeText(field.value);
    });
  }

  function humanDuration(seconds) {
    if (seconds < 60) return `${Math.max(0, seconds)} s`;
    if (seconds < 3600) return `${trimDecimal(seconds / 60)} min`;
    if (seconds < 86400) return `${trimDecimal(seconds / 3600)} h`;
    return `${trimDecimal(seconds / 86400)} d`;
  }

  function trimDecimal(value) {
    return value.toFixed(1).replace(/\.0$/, "");
  }

  function humanSize(bytes) {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${trimDecimal(bytes / 1024)} KiB`;
    return `${trimDecimal(bytes / (1024 * 1024))} MiB`;
  }

  function addDefinition(list, label, value, className = "") {
    const term = document.createElement("dt");
    term.textContent = label;
    const detail = document.createElement("dd");
    detail.textContent = value || "…";
    if (className) detail.className = className;
    list.append(term, detail);
  }

  function renderQueue(message) {
    const queue = document.querySelector("#mail-queue");
    const empty = document.querySelector("#mail-queue-empty");
    queue.replaceChildren();
    const received = Date.now();
    for (const item of message.queue ?? []) {
      const details = document.createElement("details");
      details.className = "mail-msg";
      const summary = document.createElement("summary");
      const dot = document.createElement("span");
      dot.className = "mail-queued-dot";
      dot.title = "Queued";
      const destination = document.createElement("strong");
      destination.textContent = item.to || item.id || "Queued message";
      const title = document.createElement("span");
      title.className = "mail-msg-subject";
      title.textContent = item.subject || "(no subject)";
      const size = document.createElement("span");
      size.textContent = humanSize(Number(item.size) || 0);
      const age = document.createElement("span");
      age.textContent = humanDuration(Number(item.ageSec) || 0);
      summary.append(dot, destination, title, size, age);
      const retrySeconds = item.nextRetrySec ?? message.nextRetrySec;
      if (Number.isFinite(retrySeconds)) {
        const retry = document.createElement("span");
        retry.className = "mail-retry";
        retry.dataset.deadline = String(received + Math.max(0, retrySeconds) * 1000);
        retry.title = "Countdown to the next queue flush; dma may apply its own backoff.";
        summary.append(retry);
      }
      const enriched = ["to", "subject", "preview", "lastError"].some((key) => key in item);
      if (enriched) {
        const content = document.createElement("div");
        content.className = "mail-msg-body";
        const definitions = document.createElement("dl");
        addDefinition(definitions, "From", item.from);
        addDefinition(definitions, "Recipients", (item.recipients ?? []).join(", ") || item.to);
        addDefinition(definitions, "Submitted", item.submittedTs
          ? new Date(item.submittedTs).toLocaleString() : "…");
        addDefinition(definitions, "Last error", item.lastError, "mail-last-error");
        content.append(definitions);
        const contentsTitle = document.createElement("h3");
        contentsTitle.textContent = "Contents";
        const preview = document.createElement("pre");
        preview.textContent = item.preview || "(no text/plain preview)";
        content.append(contentsTitle, preview);
        if (item.attachments?.length) {
          const attachments = document.createElement("p");
          attachments.className = "mail-attachments";
          attachments.textContent = `Attachments: ${item.attachments
            .map((part) => `${part.mimeType || "application/octet-stream"} (${humanSize(Number(part.size) || 0)})`)
            .join(", ")}`;
          content.append(attachments);
        }
        details.append(summary, content);
      } else {
        details.append(summary);
      }
      queue.append(details);
    }
    empty.hidden = queue.children.length > 0;
    const cadence = Number(message.flushEverySec);
    empty.textContent = Number.isFinite(cadence)
      ? `Queue empty. Messages deliver on submit or wait here between flush runs (every ${cadence}s).`
      : "Queue empty";
    updateCountdowns();
  }

  function updateCountdowns() {
    const now = Date.now();
    for (const node of document.querySelectorAll(".mail-retry")) {
      const seconds = Math.max(0, Math.ceil((Number(node.dataset.deadline) - now) / 1000));
      node.textContent = `retry in ${seconds}s · next flush`;
    }
  }
  setInterval(updateCountdowns, 1000);

  function renderTimeline(message) {
    const section = document.querySelector("#mail-activity-section");
    const list = document.querySelector("#mail-activity");
    if (!Array.isArray(message.timeline)) {
      section.hidden = true;
      return;
    }
    section.hidden = false;
    list.replaceChildren();
    for (const event of message.timeline.slice(0, 20)) {
      const row = document.createElement("li");
      const time = document.createElement("time");
      const eventDate = new Date(Number(event.ts));
      if (!Number.isNaN(eventDate.getTime())) {
        time.dateTime = eventDate.toISOString();
        time.textContent = eventDate.toLocaleTimeString([], { hour: "numeric", minute: "2-digit", second: "2-digit" });
      } else {
        time.textContent = "…";
      }
      const text = document.createElement("span");
      text.textContent = event.text;
      row.append(time, text);
      list.append(row);
    }
    if (!list.children.length) {
      const row = document.createElement("li");
      row.className = "mail-empty";
      row.textContent = "No queue activity yet.";
      list.append(row);
    }
  }

  function status(message) {
    document.querySelector("#mail-mode").textContent = message.mode === "smarthost" ? "Smarthost relay" : "Direct MX";
    document.querySelector("#mail-from").textContent = message.from ?? "…";
    document.querySelector("#mail-dkim").textContent = message.dkim?.enabled
      ? `${message.dkim.domain} (${message.dkim.selector})`
      : (message.dkim?.note ?? "Disabled");
    const dns = document.querySelector("#mail-dns");
    dns.hidden = !message.dkim?.enabled;
    document.querySelector("#mail-dns-name").value = message.dkim?.dnsName ?? "";
    document.querySelector("#mail-dns-value").value = message.dkim?.dnsValue ?? "";
    renderQueue(message);
    renderTimeline(message);
    const last = message.lastResult;
    document.querySelector("#mail-last").textContent = last
      ? `${last.ok ? "Accepted" : "Failed"} · ${last.to} · ${last.ts}${last.error ? ` · ${last.error}` : ""}`
      : "No sends yet in this controller session.";
  }

  return {
    connection(state) {
      live = state === "live";
      if (live) send({ type: "mail-status-req" });
      enabled();
    },
    frame(message) {
      if (message.type === "mail-status") {
        status(message);
      } else if (message.type === "mail-result" && message.id === active) {
        result.className = `mail-result ${message.ok ? "ok" : "error"}`;
        result.textContent = message.ok ? "Message accepted for delivery." : `Submission failed: ${message.error}`;
        active = null;
        enabled();
      }
    },
  };
}
