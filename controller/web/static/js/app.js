import { renderState, renderStatus } from "./render.js";
import { initCharts } from "./chart.js";
import { initChat } from "./chat.js";
import { connect } from "./ws.js";
import { initRouter } from "./router.js";
import { initNav } from "./nav.js";
import { initTheme } from "./theme.js";
import { initAgent } from "./agent.js";

initTheme();
initNav();
const charts = initCharts((value) => socket.send(value));
const chat = initChat((value) => socket.send(value));
const agent = initAgent(chat.log, (text) => chat.setStatus(text));
initRouter((page) => {
  if (page === "status") charts.draw();
});

function onStatus(status) {
  renderStatus(status);
  chat.status(status);
  charts.status(status);
}

function onMessage(message) {
  switch (message?.type) {
    case "state":
      renderState(message);
      charts.push(message);
      break;
    case "metrics":
      charts.replace(message);
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
    case "chat-stats":
      chat.stats(message);
      break;
    case "llm-status":
      chat.llm(message);
      break;
    case "agent-step":
      agent.step(message);
      break;
    case "agent-status":
      agent.status(message);
      break;
  }
}

const socket = connect(onMessage, onStatus);
