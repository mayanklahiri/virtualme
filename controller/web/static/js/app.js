import { renderState, renderStatus } from "./render.js";
import { initCharts } from "./chart.js";
import { initChat } from "./chat.js";
import { connect } from "./ws.js";
import { initRouter } from "./router.js";
import { initNav } from "./nav.js";
import { initTheme } from "./theme.js";
import { initAgent } from "./agent.js";
import { initTTS } from "./tts.js";
import { initMail } from "./mail.js";
import { initProjects } from "./projects.js";
import { initJobs } from "./jobs.js";

initTheme();
initNav();
const charts = initCharts((value) => socket.send(value));
const chat = initChat((value) => socket.send(value));
const speech = initTTS((value) => socket.send(value));
const mail = initMail((value) => socket.send(value));
const projects = initProjects((value) => socket.send(value));
const jobs = initJobs((value) => socket.send(value));
const agent = initAgent(chat.log, (text) => chat.setStatus(text));

function onStatus(status) {
  renderStatus(status);
  chat.status(status);
  charts.status(status);
  speech.status(status);
  mail.connection(status);
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
    case "tts-start":
    case "tts-chunk":
    case "tts-status":
    case "tts-done":
    case "tts-error":
      speech.frame(message);
      chat.tts(message);
      break;
    case "mail-result":
    case "mail-status":
      mail.frame(message);
      break;
    case "projects":
      projects.frame(message);
      break;
    case "project-error":
      projects.error(message.error ?? "unknown error");
      break;
    case "queue-state":
      projects.queue(message);
      jobs.frame(message);
      break;
    case "activity":
    case "activity-event":
      jobs.frame(message);
      break;
  }
}

const socket = connect(onMessage, onStatus);
initRouter((page) => {
  if (page === "status") charts.draw();
  if (page === "projects" || page === "project-detail") projects.render(page);
  if (page === "jobs") jobs.enter();
  else jobs.close();
});
