import { durationElement } from "./duration.js";

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

  const clear = document.querySelector("#mail-clear");
  let clearRevert = 0;

  function resetClear() {
    clearTimeout(clearRevert);
    clear.classList.remove("mail-clear-confirm");
    clear.textContent = "Clear queue";
  }

  clear.addEventListener("click", () => {
    if (!clear.classList.contains("mail-clear-confirm")) {
      clear.classList.add("mail-clear-confirm");
      clear.textContent = "Confirm clear";
      clearRevert = setTimeout(resetClear, 3000);
      return;
    }
    resetClear();
    send({ type: "mail-clear" });
  });

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
      title.title = item.subject || "";
      const size = document.createElement("span");
      size.textContent = humanSize(Number(item.size) || 0);
      const age = document.createElement("span");
      age.append(durationElement((Number(item.ageSec) || 0) * 1000));
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
    clear.hidden = queue.children.length === 0;
    if (clear.hidden) resetClear();
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

  const OUTBOX_LABELS = {
    queued: "queued",
    left_queue: "sent (left queue)",
    error: "error",
    cleared: "cleared",
  };

  function renderOutbox(message) {
    const list = document.querySelector("#mail-outbox");
    const empty = document.querySelector("#mail-outbox-empty");
    list.replaceChildren();
    for (const entry of (message.outbox ?? []).slice(0, 50)) {
      const row = document.createElement("li");
      row.className = "mail-outbox-row";
      const pill = document.createElement("span");
      const state = String(entry.status || "queued");
      pill.className = `mail-pill mail-pill-${state.replaceAll(/[^a-z_]/g, "")}`;
      pill.textContent = OUTBOX_LABELS[state] ?? state;
      const recipient = document.createElement("strong");
      recipient.textContent = entry.to || "…";
      recipient.title = entry.to || "";
      const title = document.createElement("span");
      title.className = "mail-msg-subject";
      title.textContent = entry.subject || "(no subject)";
      title.title = entry.subject || "";
      const age = document.createElement("span");
      age.className = "mail-outbox-age";
      age.append(durationElement(Math.max(0, Date.now() - Number(entry.ts))));
      row.append(pill, recipient, title, age);
      if (state === "error" && entry.lastError) {
        const error = document.createElement("p");
        error.className = "mail-last-error";
        error.textContent = entry.lastError;
        row.append(error);
      }
      list.append(row);
    }
    empty.hidden = list.children.length > 0;
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
    renderOutbox(message);
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
