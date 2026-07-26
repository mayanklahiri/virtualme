import { renderTree } from "./tree.js";

/**
 * @typedef {{id:string,ts?:number,outcome?:string,updateId?:number,chatLabel?:string,chatId?:string,username?:string,userId?:string,messageId?:number,detail?:string,textPreview?:string,rawOmitted?:boolean,rawUpdate?:unknown}} TelegramEvent
 * @typedef {{chatId:string,label?:string}} TelegramDestination
 * @typedef {{enabled?:boolean,state?:string,detail?:string,bot?:{displayName?:string,username?:string,id?:string},poll?:{nextOffset?:number,consecutiveFailures?:number,retryAt?:number,lastSuccessTs?:number},destinations?:TelegramDestination[]}} TelegramStatus
 */

/** @param {string} text */
export function validTestText(text) {
  const size = [...text.trim()].length;
  return size >= 1 && size <= 4096;
}

/** @param {TelegramEvent[]} current @param {TelegramEvent} incoming */
export function mergeEvents(current, incoming) {
  return [incoming, ...current.filter((item) => item.id !== incoming.id)].slice(0, 50);
}

/** @param {TelegramStatus} status @returns {[string, string][]} */
export function statusRows(status) {
  return [
    ["Detail", status.detail ?? ""],
    ["Bot", status.bot?.displayName ?? ""],
    ["Username", status.bot?.username ? `@${status.bot.username}` : ""],
    ["Bot ID", status.bot?.id ?? ""],
    ["Next offset", String(status.poll?.nextOffset ?? 0)],
    ["Failures", String(status.poll?.consecutiveFailures ?? 0)],
    ["Retry at", status.poll?.retryAt ? new Date(status.poll.retryAt).toLocaleString() : "Not scheduled"],
    ["Last poll", status.poll?.lastSuccessTs ? new Date(status.poll.lastSuccessTs).toLocaleString() : "Never"],
  ];
}

/** @param {(message: Record<string, unknown>) => void} send */
export function initTelegram(send) {
  const stateText = /** @type {HTMLElement} */ (document.querySelector("#telegram-state"));
  const details = /** @type {HTMLElement} */ (document.querySelector("#telegram-details"));
  const form = /** @type {HTMLFormElement} */ (document.querySelector("#telegram-test-form"));
  const destination = /** @type {HTMLSelectElement} */ (document.querySelector("#telegram-destination"));
  const text = /** @type {HTMLTextAreaElement} */ (document.querySelector("#telegram-test-text"));
  const button = /** @type {HTMLButtonElement} */ (document.querySelector("#telegram-test-send"));
  const result = /** @type {HTMLElement} */ (document.querySelector("#telegram-test-result"));
  const eventsCard = /** @type {HTMLElement} */ (document.querySelector("#telegram-events-card"));
  const eventList = /** @type {HTMLElement} */ (document.querySelector("#telegram-event-list"));
  const detail = /** @type {HTMLElement} */ (document.querySelector("#telegram-event-detail"));
  const detailMeta = /** @type {HTMLElement} */ (document.querySelector("#telegram-event-meta"));
  const detailError = /** @type {HTMLElement} */ (document.querySelector("#telegram-event-error"));
  const raw = /** @type {HTMLElement} */ (document.querySelector("#telegram-event-raw"));
  /** @type {TelegramStatus} */
  let status = { enabled: false, state: "disabled", destinations: [] };
  let connected = false;
  /** @type {TelegramEvent[]} */
  let events = [];
  let requestID = "";
  let detailRequestID = "";

  /** @param {string} tag @param {string} value @param {string} [className] */
  function element(tag, value, className = "") {
    const node = document.createElement(tag);
    node.textContent = value;
    if (className) node.className = className;
    return node;
  }

  function updateControls() {
    const usable = connected && status.state === "connected" && (status.destinations?.length ?? 0) > 0;
    destination.disabled = !usable;
    button.disabled = !usable || !validTestText(text.value);
  }

  function renderStatus() {
    stateText.textContent = `${status.enabled ? "Enabled" : "Disabled"} · ${status.state}`;
    details.replaceChildren();
    const fields = statusRows(status);
    for (const [label, value] of fields) details.append(element("dt", label), element("dd", value));
    const selected = destination.value;
    destination.replaceChildren();
    for (const item of status.destinations ?? []) {
      const option = document.createElement("option");
      option.value = item.chatId;
      option.textContent = `${item.label ?? `Chat ${item.chatId}`} (${item.chatId})`;
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
        element("time", event.ts ? new Date(event.ts).toLocaleString() : ""),
        element("strong", event.outcome ?? ""),
        element("span", event.chatLabel || event.chatId || ""),
        element("span", event.username || event.userId || ""),
        element("span", event.textPreview || event.detail || ""),
      );
      row.addEventListener("click", () => {
        detailRequestID = crypto.randomUUID();
        send({ type: "telegram-event-detail-req", requestId: detailRequestID, id: event.id });
      });
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
  document.querySelector("#telegram-event-close")?.addEventListener("click", () => {
    detail.hidden = true;
    detail.classList.remove("open");
    document.body.classList.remove("telegram-detail-locked");
    detailRequestID = "";
  });

  return {
    /** @param {string} value */
    connection(value) { connected = value === "connected"; updateControls(); },
    /** @param {string} page */
    show(page) {
      if (page === "telegram") {
        send({ type: "telegram-status-req" });
        send({ type: "telegram-events-req" });
      }
    },
    /** @param {any} message */
    frame(message) {
      if (message.type === "telegram-status") {
        status = message.status;
        renderStatus();
      } else if (message.type === "telegram-events") {
        events = message.events ?? [];
        renderEvents();
      } else if (message.type === "telegram-event") {
        events = mergeEvents(events, message.event);
        renderEvents();
      } else if (message.type === "telegram-event-detail" && message.requestId === detailRequestID) {
        detail.hidden = false;
        detail.classList.add("open");
        document.body.classList.add("telegram-detail-locked");
        detailMeta.replaceChildren();
        detailError.hidden = !message.error;
        detailError.textContent = message.error ?? "";
        raw.replaceChildren();
        if (message.event) {
          for (const [label, value] of [
            ["Outcome", message.event.outcome], ["Update ID", message.event.updateId],
            ["Chat", message.event.chatLabel || message.event.chatId],
            ["User", message.event.username || message.event.userId],
            ["Message ID", message.event.messageId], ["Detail", message.event.detail],
          ]) detailMeta.append(element("dt", label), element("dd", String(value ?? "")));
          if (message.event.rawOmitted) raw.append(element("p", "Raw update exceeded the 16 KiB retention cap."));
          else raw.append(renderTree(message.event.rawUpdate ?? {}, { expandDepth: 0 }));
        }
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
