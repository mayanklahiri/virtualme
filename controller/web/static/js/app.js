import { renderState } from "./render.js";
import { initConnectionWatch } from "./conn.js";
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
import { initTools } from "./tools.js";
import { initData } from "./data.js";
import { initConfig } from "./config.js";
import { initNotifications } from "./notifications.js";
import { initTelegram } from "./telegram.js";

initTheme();
initNav();
const connectionWatch = initConnectionWatch();
const charts = initCharts((value) => socket.send(value));
const chat = initChat((value) => socket.send(value));
const speech = initTTS((value) => socket.send(value));
const mail = initMail((value) => socket.send(value));
const projects = initProjects((value) => socket.send(value));
const jobs = initJobs((value) => socket.send(value));
const tools = initTools((value) => socket.send(value));
const data = initData();
const configuration = initConfig();
const notifications = initNotifications((value) => socket.send(value));
const telegram = initTelegram((value) => socket.send(value));
const agent = initAgent(chat.log, (text) => chat.setStatus(text));
const jigglerSwitch = document.querySelector("#jiggler-switch");
jigglerSwitch.addEventListener("click", () => {
  socket.send({
    type: "jiggler-set",
    enabled: jigglerSwitch.getAttribute("aria-checked") !== "true",
  });
});
const schedulerSwitch = document.querySelector("#scheduler-switch");
schedulerSwitch.addEventListener("click", () => {
  // Cockpit polarity: the lamp is lit while the scheduler runs, so pressing
  // a lit button pauses it. scheduler-set carries "enabled" (running).
  socket.send({
    type: "scheduler-set",
    enabled: schedulerSwitch.getAttribute("aria-checked") !== "true",
  });
});
// Tap/click a label to pin its tooltip open on touch devices.
for (const label of document.querySelectorAll(".qo-label")) {
  label.addEventListener("click", () => {
    label.closest(".qo-cell")?.classList.toggle("qo-open");
  });
}

function onStatus(status, connectedSince) {
  connectionWatch.status(status, connectedSince);
  chat.status(status);
  charts.status(status);
  speech.status(status);
  mail.connection(status);
  configuration.connection(status);
  notifications.status(status);
  telegram.connection(status);
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
    case "speech-log":
      speech.frame(message);
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
    case "tools-list":
    case "tool-result":
      tools.frame(message);
      break;
    case "config-saved":
      configuration.saved(message);
      break;
    case "config-restarting":
      configuration.restarting(message);
      break;
    case "notifications-state":
    case "notifications-page":
    case "notification-detail":
    case "notification-error":
      notifications.frame(message);
      break;
    case "telegram-status":
    case "telegram-events":
    case "telegram-event":
    case "telegram-event-detail":
    case "telegram-command-result":
    case "telegram-userlist":
      telegram.frame(message);
      break;
  }
}

const socket = connect(onMessage, onStatus);
initRouter((page) => {
  if (page === "status") charts.draw();
  if (page === "projects" || page === "project-detail") projects.render(page);
  if (page === "jobs") jobs.enter();
  else jobs.close();
  if (page === "tools") tools.enter();
  data.show(page);
  configuration.show(page);
  if (page === "notifications") notifications.enter();
  else notifications.closePopover(false);
  telegram.show(page);
});
