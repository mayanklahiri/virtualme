// Zero-dependency websocket probe for persistent projects.
const args = process.argv.slice(2);
const verify = args[0] === "--verify-delete";
const idArg = verify ? args[1] : "";
const url = verify ? args[2] : args[0];
if (!url || (verify && !idArg)) {
  console.error("usage: projects-probe.mjs [--verify-delete <id>] <ws-url>");
  process.exit(2);
}

const name = `E2E project ${Date.now()}`;
let projectId = idArg;
let phase = verify ? "verify" : "create";
let lifecycleSeen = false;
let summarySeen = false;
const timer = setTimeout(() => fail("timeout after 300s"), 300000);
const socket = new WebSocket(url);

/**
 * @typedef {{id:string,name:string,selector:string}} ProbeProject
 * @typedef {{type?:string,error?:string,projects?:ProbeProject[],runs?:Record<string,unknown[]>,running?:{type?:string,projectId?:string}|null,upcoming?:Array<{type?:string,projectId?:string}>,finished?:Array<{type?:string,projectId?:string}>}} ProbeMessage
 */

/** @param {string} message */
function fail(message) {
  clearTimeout(timer);
  console.error(`projects-probe: FAIL: ${message}`);
  process.exit(1);
}

/** @param {Record<string, unknown>} value */
function send(value) {
  socket.send(JSON.stringify(value));
}

/** @param {string} message */
function finish(message) {
  clearTimeout(timer);
  console.log(message);
  process.exit(0);
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
    const all = [message.running, ...(message.upcoming ?? []), ...(message.finished ?? [])].filter(Boolean);
    if (all.some((job) => job?.type === "project-run" && job.projectId === projectId)) {
      lifecycleSeen = true;
    }
    if (summarySeen && lifecycleSeen) finish(`projects-probe: OK id=${projectId}`);
    return;
  }
  if (message.type !== "projects") return;
  const projects = message.projects ?? [];

  if (phase === "verify") {
    if (!projects.some((project) => project.id === projectId)) {
      fail("project did not survive restart");
    }
    phase = "delete";
    send({ type: "project-delete", id: projectId });
    return;
  }
  if (phase === "delete") {
    if (projects.some((project) => project.id === projectId)) return;
    finish(`projects-probe: OK persisted-and-deleted id=${projectId}`);
  }
  if (phase === "create") {
    phase = "created";
    send({ type: "project-create", name });
    return;
  }
  if (phase === "created") {
    const project = projects.find((entry) => entry.name === name);
    if (!project) return;
    projectId = project.id;
    phase = "updated";
    send({
      type: "project-update",
      id: projectId,
      task: "Reply with a one-sentence confirmation. Do not use tools.",
      selector: "tue,thu morning",
      enabled: false,
    });
    return;
  }
  if (phase === "updated") {
    const project = projects.find((entry) => entry.id === projectId);
    if (!project || project.selector !== "tue,thu morning") return;
    phase = "running";
    send({ type: "project-run", id: projectId });
    send({ type: "queue-peek" });
    return;
  }
  if (phase === "running") {
    const summaries = message.runs?.[projectId] ?? [];
    if (summaries.length === 0) return;
    summarySeen = true;
    send({ type: "queue-peek" });
    if (lifecycleSeen) finish(`projects-probe: OK id=${projectId}`);
  }
});
