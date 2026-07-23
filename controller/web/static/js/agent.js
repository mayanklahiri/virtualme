function toolIcon(tool) {
  if (tool === "bash" || tool === "shell") {
    return "terminal";
  }
  if (tool.includes("browser")) {
    return "monitor";
  }
  return "bot";
}

export function initAgent(log, setStatus) {
  const groups = new Map();
  return {
    step(message) {
      const taskId = String(message.taskId ?? "");
      let group = groups.get(taskId);
      if (!group || !group.isConnected) {
        group = document.createElement("li");
        group.className = "agent-group";
        group.dataset.taskId = taskId;
        const users = log.querySelectorAll(".msg.user");
        const trigger = users[users.length - 1];
        if (trigger) {
          trigger.after(group);
        } else {
          log.append(group);
        }
        groups.set(taskId, group);
      }
      const details = document.createElement("details");
      details.className = "agent-step";
      details.dataset.taskId = taskId;
      const summary = document.createElement("summary");
      const svg = document.createElementNS("http:" + "//www.w3.org/2000/svg", "svg");
      svg.classList.add("icon");
      const use = document.createElementNS("http:" + "//www.w3.org/2000/svg", "use");
      use.setAttribute("href", `/icons.svg#i-${toolIcon(String(message.tool ?? ""))}`);
      svg.append(use);
      summary.append(svg, document.createTextNode(`Step ${message.n ?? "?"}: ${message.summary ?? message.tool ?? "Agent action"}`));
      const pre = document.createElement("pre");
      pre.textContent = JSON.stringify(message.args ?? {}, null, 2);
      details.append(summary, pre);
      if (typeof message.screenshot === "string" && message.screenshot.startsWith("data:image/jpeg;base64,")) {
        const image = document.createElement("img");
        image.src = message.screenshot;
        image.alt = `Agent step ${message.n ?? ""} screenshot`;
        details.append(image);
      }
      group.append(details);
      log.scrollTop = log.scrollHeight;
    },
    status(message) {
      const labels = {
        planning: "agent planning…",
        acting: `agent acting — step ${message.n ?? ""}`,
        observing: `agent observing — step ${message.n ?? ""}`,
        done: "",
        failed: "agent failed",
        stopped: "agent stopped",
      };
      setStatus(labels[message.phase] ?? "");
    },
  };
}
