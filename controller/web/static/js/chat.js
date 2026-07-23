const MAX_LEN = 4096;

export function initChat(send) {
  const log = document.querySelector("#chat-log");
  const form = document.querySelector("#chat-form");
  const input = document.querySelector("#chat-input");
  const button = document.querySelector("#chat-send");
  const counter = document.querySelector("#chat-count");
  let busy = false;
  let live = false;
  let streaming = null; // the assistant <li> currently receiving deltas

  function setEnabled() {
    const enabled = live && !busy;
    input.disabled = !enabled;
    button.disabled = !enabled;
  }

  function nearBottom() {
    return log.scrollHeight - log.scrollTop - log.clientHeight < 40;
  }

  function append(item) {
    const follow = nearBottom();
    log.append(item);
    if (follow) {
      log.scrollTop = log.scrollHeight;
    }
  }

  function addMessage(role, text) {
    const item = document.createElement("li");
    item.className = `msg ${role}`;
    item.textContent = text;
    append(item);
    return item;
  }

  function updateCounter() {
    counter.textContent = `${input.value.length} / ${MAX_LEN}`;
  }

  function finishStreaming() {
    if (streaming !== null && streaming.textContent === "") {
      streaming.remove();
    }
    streaming = null;
    busy = false;
    setEnabled();
  }

  form.addEventListener("submit", (event) => {
    event.preventDefault();
    const text = input.value.trim();
    if (text === "" || text.length > MAX_LEN || busy || !live) {
      return;
    }
    if (send({ type: "chat", text })) {
      busy = true;
      setEnabled();
      input.value = "";
      updateCounter();
    }
  });
  input.addEventListener("keydown", (event) => {
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      form.requestSubmit();
    }
  });
  input.addEventListener("input", updateCounter);
  updateCounter();

  return {
    status(status) {
      live = status === "live";
      setEnabled();
    },
    history(messages) {
      log.replaceChildren();
      streaming = null;
      for (const message of messages) {
        addMessage(message.role === "user" ? "user" : "assistant", message.text ?? "");
      }
      log.scrollTop = log.scrollHeight;
    },
    message(message) {
      addMessage(message.role === "user" ? "user" : "assistant", message.text ?? "");
      if (message.role === "user") {
        busy = true;
        setEnabled();
        streaming = addMessage("assistant streaming", "");
      }
    },
    delta(text) {
      if (streaming === null) {
        streaming = addMessage("assistant streaming", "");
      }
      const follow = nearBottom();
      streaming.textContent += text;
      if (follow) {
        log.scrollTop = log.scrollHeight;
      }
    },
    done(message) {
      if (streaming !== null) {
        streaming.textContent = message.text ?? streaming.textContent;
        streaming.className = "msg assistant";
        streaming = null;
      } else {
        addMessage("assistant", message.text ?? "");
      }
      busy = false;
      setEnabled();
    },
    error(text) {
      const item = document.createElement("li");
      item.className = "msg notice";
      const label = document.createElement("span");
      label.textContent = text;
      const dismiss = document.createElement("button");
      dismiss.type = "button";
      dismiss.textContent = "Dismiss";
      dismiss.addEventListener("click", () => item.remove());
      item.append(label, dismiss);
      append(item);
      finishStreaming();
    },
  };
}
