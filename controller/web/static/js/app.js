import { renderState, renderStatus } from "./render.js";
import { initCharts } from "./chart.js";
import { initChat } from "./chat.js";
import { connect } from "./ws.js";

const charts = initCharts();
const chat = initChat((value) => socket.send(value));

function onStatus(status) {
  renderStatus(status);
  chat.status(status);
}

function onMessage(message) {
  switch (message?.type) {
    case "state":
      renderState(message);
      charts.push(message);
      break;
    case "history":
      charts.replace(message.snapshots ?? []);
      break;
    case "chat-history":
      chat.history(message.messages ?? []);
      break;
    case "chat-message":
      chat.message(message);
      break;
    case "chat-delta":
      chat.delta(message.text ?? "");
      break;
    case "chat-done":
      chat.done(message);
      break;
    case "chat-error":
      chat.error(message.error ?? "unknown error");
      break;
  }
}

const socket = connect(onMessage, onStatus);
