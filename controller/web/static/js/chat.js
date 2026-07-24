import { renderMarkdown } from "./markdown.js";
import { AudioPlayer } from "./audio-player.js";

const MAX_LEN = 4096;

function svgIcon(name) {
  const svg = document.createElementNS("http:" + "//www.w3.org/2000/svg", "svg");
  svg.classList.add("icon");
  svg.setAttribute("aria-hidden", "true");
  const use = document.createElementNS("http:" + "//www.w3.org/2000/svg", "use");
  use.setAttribute("href", `/icons.svg#i-${name}`);
  svg.append(use);
  return svg;
}

export function initChat(send) {
  const log = document.querySelector("#chat-log");
  const form = document.querySelector("#chat-form");
  const input = document.querySelector("#chat-input");
  const sendButton = document.querySelector("#chat-send");
  const stopButton = document.querySelector("#chat-stop");
  const clearButton = document.querySelector("#chat-clear");
  const clearLabel = clearButton.querySelector("span");
  const counter = document.querySelector("#chat-count");
  const stats = document.querySelector("#chat-stats");
  const statusLine = document.querySelector("#llm-status");
  let busy = false;
  let live = false;
  let streaming = null;
  let clearTimer;
  const audio = new Map();

  function setEnabled() {
    input.disabled = !live || busy;
    sendButton.disabled = !live || busy;
    stopButton.hidden = !busy;
    clearButton.disabled = !live || busy;
  }
  function nearBottom() {
    return log.scrollHeight - log.scrollTop - log.clientHeight < 40;
  }
  function append(item) {
    const follow = nearBottom();
    log.append(item);
    if (follow) log.scrollTop = log.scrollHeight;
  }
  function copyButton(text) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "message-copy";
    button.setAttribute("aria-label", "Copy message");
    button.append(svgIcon("copy"));
    button.addEventListener("click", async () => {
      await navigator.clipboard.writeText(text);
      button.replaceChildren(svgIcon("check"));
      setTimeout(() => button.replaceChildren(svgIcon("copy")), 1500);
    });
    return button;
  }
  function addMessage(role, text, markdown = role === "assistant", stopped = false) {
    const item = document.createElement("li");
    item.className = `msg ${role}`;
    const body = document.createElement("div");
    body.className = "message-body";
    if (markdown) body.append(renderMarkdown(text)); else body.textContent = text;
    item.append(body, copyButton(text));
    if (stopped) {
      const marker = document.createElement("small");
      marker.className = "stopped";
      marker.textContent = "stopped";
      item.append(marker);
    }
    append(item);
    return { item, body };
  }
  function finish() {
    if (streaming?.body.textContent === "") streaming.item.remove();
    streaming = null;
    busy = false;
    setEnabled();
  }
  function updateCounter() {
    counter.textContent = `${input.value.length} / ${MAX_LEN}`;
  }

  form.addEventListener("submit", (event) => {
    event.preventDefault();
    const text = input.value.trim();
    if (!text || text.length > MAX_LEN || busy || !live) return;
    if (send({ type: "chat", text })) {
      busy = true;
      input.value = "";
      updateCounter();
      setEnabled();
    }
  });
  input.addEventListener("keydown", (event) => {
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      form.requestSubmit();
    }
  });
  input.addEventListener("input", updateCounter);
  stopButton.addEventListener("click", () => send({ type: "chat-stop" }));
  clearButton.addEventListener("click", () => {
    if (!clearButton.classList.contains("armed")) {
      clearButton.classList.add("armed");
      clearLabel.textContent = "Sure?";
      clearTimeout(clearTimer);
      clearTimer = setTimeout(() => {
        clearButton.classList.remove("armed");
        clearLabel.textContent = "Clear";
      }, 3000);
      return;
    }
    clearTimeout(clearTimer);
    clearButton.classList.remove("armed");
    clearLabel.textContent = "Clear";
    send({ type: "chat-clear" });
  });
  updateCounter();

  return {
    log,
    setStatus(text) {
      statusLine.textContent = text;
    },
    status(status) {
      live = status === "live";
      setEnabled();
    },
    history(messages) {
      log.replaceChildren();
      streaming = null;
      busy = false;
      for (const message of messages) {
        addMessage(message.role === "user" ? "user" : "assistant", message.text ?? "");
      }
      log.scrollTop = log.scrollHeight;
      setEnabled();
    },
    message(message) {
      addMessage(message.role === "user" ? "user" : "assistant", message.text ?? "", message.role !== "user");
      if (message.role === "user") {
        busy = true;
        setEnabled();
        streaming = addMessage("assistant streaming", "", false);
      }
    },
    delta(text) {
      if (!streaming) streaming = addMessage("assistant streaming", "", false);
      const follow = nearBottom();
      streaming.body.textContent += text;
      if (follow) log.scrollTop = log.scrollHeight;
    },
    done(message) {
      const text = message.text ?? streaming?.body.textContent ?? "";
      if (streaming) {
        streaming.body.replaceChildren(renderMarkdown(text));
        streaming.item.className = "msg assistant";
        streaming.item.querySelector(".message-copy")?.remove();
        streaming.item.append(copyButton(text));
        if (message.stopped) {
          const marker = document.createElement("small");
          marker.className = "stopped";
          marker.textContent = "stopped";
          streaming.item.append(marker);
        }
      } else {
        addMessage("assistant", text, true, Boolean(message.stopped));
      }
      streaming = null;
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
      finish();
    },
    stats(message) {
      stats.textContent = `${message.queries ?? 0} queries · ${message.promptTokens ?? 0} prompt + ${message.completionTokens ?? 0} completion tokens · ${((message.genMs ?? 0) / 1000).toFixed(1)} s thinking`;
    },
    llm(message) {
      const elapsed = ((message.elapsedMs ?? 0) / 1000).toFixed(1);
      const labels = {
        idle: "",
        sending: "sending…",
        queued: message.detail ? `${message.detail}…` : "queued…",
        processing: `reading prompt (${message.promptN ?? 0}/${message.promptTotal ?? 0})…`,
        generating: `generating — ${message.tokens ?? 0} tokens · ${Number(message.tokPerSec ?? 0).toFixed(1)} tok/s · ${elapsed}s`,
      };
      statusLine.textContent = labels[message.phase] ?? "";
    },
    async tts(message) {
      if (message.origin !== "chat") return;
      let entry = audio.get(message.id);
      if (message.type === "tts-start") {
        const item = document.createElement("li");
        item.className = "msg assistant audio-bubble";
        const icon = svgIcon("volume-2");
        const label = document.createElement("span");
        label.textContent = `speaking · sentence 1/${message.sentences}`;
        const replay = document.createElement("button");
        replay.type = "button";
        replay.hidden = true;
        replay.append(svgIcon("play"), document.createTextNode("Replay"));
        const player = new AudioPlayer();
        replay.addEventListener("click", () => player.replay());
        item.append(icon, label, replay);
        append(item);
        entry = { player, label, replay, sentences: message.sentences };
        audio.set(message.id, entry);
        await player.begin(message.sampleRate);
      } else if (!entry) {
        return;
      }
      if (message.type === "tts-chunk") {
        entry.player.push(message.pcm);
      } else if (message.type === "tts-status" && message.phase === "synthesizing") {
        entry.label.textContent = `speaking · sentence ${message.sentence}/${message.sentences}`;
      } else if (message.type === "tts-done") {
        entry.label.textContent = `${Number(message.audioSec).toFixed(1)} s audio`;
        entry.replay.hidden = false;
      } else if (message.type === "tts-error") {
        entry.label.textContent = `speech failed: ${message.error}`;
        entry.player.stop();
      }
    },
  };
}
