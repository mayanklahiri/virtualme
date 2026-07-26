// Zero-dependency live probe for scheduler pickup of a persisted project.
import { clearInterval, setInterval } from "node:timers";

const url = process.argv[2];
if (!url) {
  console.error("usage: projects-scheduler-probe.mjs <ws-url>");
  process.exit(2);
}

const name = `Scheduler probe ${Date.now()}`;
let projectId = "";
let phase = "initial";
let scheduledSeen = false;
let tokenSeen = false;
const timer = setTimeout(() => fail("timeout after 150s"), 150000);
const poll = setInterval(() => {
  if (projectId) send({ type: "queue-peek" });
}, 1000);
const socket = new WebSocket(url);

/**
 * @typedef {{id:string,name:string,task:string,selector:string,enabled:boolean,lastEnqueuedBucket:string}} ProbeProject
 * @typedef {{type?:string,error?:string,projects?:ProbeProject[],running?:ProbeJob|null,upcoming?:ProbeJob[],finished?:ProbeJob[]}} ProbeMessage
 * @typedef {{type?:string,priority?:string,projectId?:string}} ProbeJob
 */

/** @param {string} message @returns {never} */
function fail(message) {
  clearTimeout(timer);
  clearInterval(poll);
  console.error(`projects-scheduler-probe: FAIL: ${message}`);
  process.exit(1);
}

/** @param {Record<string, unknown>} value */
function send(value) {
  if (socket.readyState === WebSocket.OPEN) socket.send(JSON.stringify(value));
}

function finish() {
  clearTimeout(timer);
  clearInterval(poll);
  console.log(`projects-scheduler-probe: OK id=${projectId}`);
  process.exit(0);
}

function maybeDelete() {
  if (phase === "waiting" && scheduledSeen && tokenSeen) {
    phase = "deleting";
    send({ type: "project-delete", id: projectId });
  }
}

socket.addEventListener("error", () => fail("websocket error"));
socket.addEventListener("open", () => send({ type: "projects-req" }));
socket.addEventListener("message", (event) => {
  /** @type {ProbeMessage} */
  let message;
  try {
    message = JSON.parse(String(event.data));
  } catch {
    return;
  }
  if (message.type === "project-error") fail(message.error ?? "unknown project error");
  if (message.type === "queue-state" && projectId) {
    const jobs = [message.running, ...(message.upcoming ?? []), ...(message.finished ?? [])].filter(Boolean);
    scheduledSeen ||= jobs.some((job) =>
      job?.type === "project-run" && job.projectId === projectId && job.priority === "scheduled");
    if (scheduledSeen) send({ type: "projects-req" });
    maybeDelete();
    return;
  }
  if (message.type !== "projects") return;
  const projects = message.projects ?? [];
  if (phase === "initial") {
    phase = "creating";
    send({ type: "project-create", name });
    return;
  }
  if (phase === "creating") {
    const project = projects.find((entry) => entry.name === name);
    if (!project) return;
    projectId = project.id;
    phase = "waiting";
    send({
      type: "project-update",
      id: projectId,
      task: "Confirm that the scheduler routed this periodic project to the project action queue.",
      selector: "anytime",
      enabled: true,
    });
    return;
  }
  const project = projects.find((entry) => entry.id === projectId);
  if (phase === "deleting") {
    if (!project) finish();
    return;
  }
  if (!project) fail("project disappeared before scheduler pickup");
  tokenSeen ||= Boolean(project.lastEnqueuedBucket);
  maybeDelete();
});
