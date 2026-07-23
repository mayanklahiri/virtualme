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
    for (const field of [to, subject, body, image]) field.disabled = busy;
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

  function status(message) {
    document.querySelector("#mail-mode").textContent = message.mode === "smarthost" ? "Smarthost relay" : "Direct MX";
    document.querySelector("#mail-from").textContent = message.from ?? "—";
    document.querySelector("#mail-dkim").textContent = message.dkim?.enabled
      ? `${message.dkim.domain} (${message.dkim.selector})`
      : (message.dkim?.note ?? "Disabled");
    const dns = document.querySelector("#mail-dns");
    dns.hidden = !message.dkim?.enabled;
    document.querySelector("#mail-dns-name").value = message.dkim?.dnsName ?? "";
    document.querySelector("#mail-dns-value").value = message.dkim?.dnsValue ?? "";
    const queue = document.querySelector("#mail-queue");
    queue.replaceChildren();
    for (const item of message.queue ?? []) {
      const row = document.createElement("tr");
      for (const value of [item.id, `${item.size} B`, `${item.ageSec} s`]) {
        const cell = document.createElement("td");
        cell.textContent = String(value);
        row.append(cell);
      }
      queue.append(row);
    }
    if (!queue.children.length) {
      const row = document.createElement("tr");
      const cell = document.createElement("td");
      cell.colSpan = 3;
      cell.textContent = "Queue empty";
      row.append(cell);
      queue.append(row);
    }
    const last = message.lastResult;
    document.querySelector("#mail-last").textContent = last
      ? `${last.ok ? "Accepted" : "Failed"} · ${last.to} · ${last.ts}${last.error ? ` · ${last.error}` : ""}`
      : "No messages submitted in this controller session.";
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
