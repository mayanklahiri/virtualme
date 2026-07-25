// Master-configuration websocket/API probe (Node >= 22, zero dependencies).
//
//   node test/config-probe.mjs --preflight-failure <http-base>
//   node test/config-probe.mjs --restart <http-base>

const mode = process.argv[2];
const base = process.argv[3];
if (!["--preflight-failure", "--restart"].includes(mode) || !base) {
  console.error("usage: config-probe.mjs --preflight-failure|--restart <http-base>");
  process.exit(2);
}

const wsURL = base.replace(/^http/, "ws") + "/ws";
const timeout = setTimeout(() => fail("timeout"), 30_000);
const socket = new WebSocket(wsURL);
let savedFrames = 0;
let restartingFrame = false;
let restartAccepted = false;

/** @param {string} reason */
function fail(reason) {
  console.error(`config-probe: FAIL: ${reason}`);
  process.exit(1);
}

/**
 * @param {string} path
 * @param {RequestInit} [options]
 */
async function jsonRequest(path, options = {}) {
  const response = await fetch(base + path, {
    ...options,
    headers: { "Content-Type": "application/json", ...(options.headers ?? {}) },
  });
  let body;
  try {
    body = await response.json();
  } catch {
    fail(`${path} returned non-JSON status ${response.status}`);
  }
  return { response, body };
}

async function run() {
  const initial = await jsonRequest("/api/config");
  if (!initial.response.ok) fail(`GET config status ${initial.response.status}`);
  const raw = structuredClone(initial.body.raw);

  if (mode === "--preflight-failure") {
    raw.agent.keepTasks += 1;
    const save = await jsonRequest("/api/config", {
      method: "PUT",
      body: JSON.stringify({ baseHash: initial.body.fileHash, config: raw }),
    });
    if (!save.response.ok || !save.body.pendingRestart) fail(`save failed: ${JSON.stringify(save.body)}`);
    const restart = await jsonRequest("/api/config/restart", {
      method: "POST",
      body: JSON.stringify({ pendingHash: save.body.fileHash }),
    });
    if (restart.response.status !== 503 || restart.body.error?.code !== "config_preflight_failed") {
      fail(`preflight failure status/body: ${restart.response.status} ${JSON.stringify(restart.body)}`);
    }
    setTimeout(() => {
      if (restartingFrame) fail("received restarting frame after failed preflight");
      clearTimeout(timeout);
      console.log("config-probe: preflight failure stayed pending without restart");
      process.exit(0);
    }, 750);
    return;
  }

  raw.llama.contextTokens += 1;
  const first = await jsonRequest("/api/config", {
    method: "PUT",
    body: JSON.stringify({ baseHash: initial.body.fileHash, config: raw }),
  });
  if (!first.response.ok) fail(`first save: ${JSON.stringify(first.body)}`);
  raw.agent.maxSteps += 1;
  const second = await jsonRequest("/api/config", {
    method: "PUT",
    body: JSON.stringify({ baseHash: first.body.fileHash, config: raw }),
  });
  if (!second.response.ok ||
      !second.body.restartServices.includes("llama") ||
      !second.body.restartServices.includes("controller")) {
    fail(`cumulative save plan: ${JSON.stringify(second.body)}`);
  }
  const stale = await jsonRequest("/api/config", {
    method: "PUT",
    body: JSON.stringify({ baseHash: initial.body.fileHash, config: raw }),
  });
  if (stale.response.status !== 409 || stale.body.error?.code !== "config_conflict") {
    fail(`stale save: ${stale.response.status} ${JSON.stringify(stale.body)}`);
  }
  const current = await jsonRequest("/api/config");
  if (current.body.fileHash !== second.body.fileHash) fail("stale save changed current bytes");
  const restart = await jsonRequest("/api/config/restart", {
    method: "POST",
    body: JSON.stringify({ pendingHash: second.body.fileHash }),
  });
  if (restart.response.status !== 202 || !restart.body.restarting) {
    fail(`restart response: ${restart.response.status} ${JSON.stringify(restart.body)}`);
  }
  restartAccepted = true;
}

socket.addEventListener("open", () => run().catch((error) => fail(error.stack ?? error.message)));
socket.addEventListener("error", () => {
  if (!restartAccepted) fail("websocket error");
});
socket.addEventListener("message", (event) => {
  let message;
  try {
    message = JSON.parse(String(event.data));
  } catch {
    return;
  }
  if (message.type === "config-saved") savedFrames += 1;
  if (message.type === "config-restarting") {
    restartingFrame = message.services?.includes("llama") && message.services?.includes("controller");
  }
});
socket.addEventListener("close", () => {
  if (mode === "--preflight-failure") fail("websocket closed after failed preflight");
  if (!restartAccepted || savedFrames < 2 || !restartingFrame) {
    fail(`missing response/broadcast evidence accepted=${restartAccepted} saves=${savedFrames} restarting=${restartingFrame}`);
  }
  clearTimeout(timeout);
  console.log("config-probe: saves, conflict, restart frame, and disconnect OK");
  process.exit(0);
});
