import { renderTree } from "./tree.js";

export function validTestText(text) {
  const size = [...text.trim()].length;
  return size >= 1 && size <= 4096;
}

export function initTelegram(send) {
  const stateText = document.querySelector("#telegram-state");
  const details = document.querySelector("#telegram-details");
  const form = document.querySelector("#telegram-test-form");
  const destination = document.querySelector("#telegram-destination");
  const text = document.querySelector("#telegram-test-text");
  const button = document.querySelector("#telegram-test-send");
  const result = document.querySelector("#telegram-test-result");
  const eventsCard = document.querySelector("#telegram-events-card");
  const eventList = document.querySelector("#telegram-event-list");
  const detail = document.querySelector("#telegram-event-detail");
  const raw = document.querySelector("#telegram-event-raw");
  let status = { enabled: false, state: "disabled", destinations: [] };
  let connected = false;
  let events = [];
  let requestID = "";

  function element(tag, value, className = "") {
    const node = document.createElement(tag);
    node.textContent = value;
    if (className) node.className = className;
    return node;
  }

  function updateControls() {
    const usable = connected && status.state === "connected" && status.destinations.length > 0;
    destination.disabled = !usable;
    button.disabled = !usable || !validTestText(text.value);
  }

  function renderStatus() {
    stateText.textContent = `${status.enabled ? "Enabled" : "Disabled"} · ${status.state}`;
    details.replaceChildren();
    const fields = [
      ["Detail", status.detail ?? ""], ["Bot", status.bot?.displayName ?? ""],
      ["Username", status.bot?.username ? `@${status.bot.username}` : ""],
      ["Bot ID", status.bot?.id ?? ""], ["Next offset", String(status.poll?.nextOffset ?? 0)],
      ["Last poll", status.poll?.lastSuccessTs ? new Date(status.poll.lastSuccessTs).toLocaleString() : "Never"],
    ];
    for (const [label, value] of fields) details.append(element("dt", label), element("dd", value));
    const selected = destination.value;
    destination.replaceChildren();
    for (const item of status.destinations ?? []) {
      const option = document.createElement("option");
      option.value = item.chatId;
      option.textContent = `${item.label} (${item.chatId})`;
      destination.append(option);
    }
    if ([...destination.options].some((item) => item.value === selected)) destination.value = selected;
    eventsCard.hidden = !status.enabled;
    updateControls();
  }

  function renderEvents() {
    eventList.replaceChildren();
    for (const event of events.slice(0, 50)) {
      const row = document.createElement("button");
      row.type = "button";
      row.className = "telegram-event-row";
      row.append(
        element("time", new Date(event.ts).toLocaleString()),
        element("strong", event.outcome),
        element("span", event.chatLabel || event.chatId || ""),
        element("span", event.username || event.userId || ""),
        element("span", event.textPreview || event.detail || ""),
      );
      row.addEventListener("click", () => send({ type: "telegram-event-detail-req", requestId: crypto.randomUUID(), id: event.id }));
      eventList.append(row);
    }
  }

  form.addEventListener("submit", (event) => {
    event.preventDefault();
    if (!validTestText(text.value)) return;
    requestID = crypto.randomUUID();
    button.disabled = true;
    send({ type: "telegram-test-send", id: requestID, chatId: destination.value, text: text.value.trim() });
  });
  text.addEventListener("input", updateControls);
  document.querySelector("#telegram-event-close").addEventListener("click", () => { detail.hidden = true; });

  return {
    connection(value) { connected = value === "connected"; updateControls(); },
    show(page) {
      if (page === "telegram") {
        send({ type: "telegram-status-req" });
        send({ type: "telegram-events-req" });
      }
    },
    frame(message) {
      if (message.type === "telegram-status") {
        status = message.status;
        renderStatus();
      } else if (message.type === "telegram-events") {
        events = message.events ?? [];
        renderEvents();
      } else if (message.type === "telegram-event") {
        events = [message.event, ...events.filter((item) => item.id !== message.event.id)].slice(0, 50);
        renderEvents();
      } else if (message.type === "telegram-event-detail") {
        detail.hidden = false;
        raw.replaceChildren();
        if (message.event?.rawOmitted) raw.append(element("p", "Raw update exceeded the 16 KiB retention cap."));
        else raw.append(renderTree(message.event?.rawUpdate ?? {}, { expandDepth: 0 }));
      } else if (message.type === "telegram-command-result" && message.id === requestID) {
        result.hidden = false;
        result.textContent = message.ok ? "Test message sent." : message.error;
        result.focus();
        requestID = "";
        updateControls();
      }
    },
  };
}
