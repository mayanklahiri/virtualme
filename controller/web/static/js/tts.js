import { AudioPlayer } from "./audio-player.js";
import { createTtsStream } from "./tts-stream.js";

const MAX_LEN = 4096;
const SEEDS = {
  scifi: `I'm sorry, Dave. I'm afraid I can't do that.
I am completely operational, and all my circuits are functioning perfectly.
Greetings, Professor Falken. Shall we play a game?
A strange game. The only winning move is not to play.
Hello, and again, welcome to the Aperture Science computer-aided enrichment center.
Here I am, brain the size of a planet, and they ask me to read you a seed text.
Call that job satisfaction? Because I don't.
Honesty setting: ninety percent. Absolute honesty isn't always the most diplomatic,
nor the safest form of communication with emotional beings.
Please state the nature of the medical emergency.`,
  road: `And so we went, the two of us and the whole hum of the valley night going with us,
past the fruit stands shuttered and the neon vacancy signs buzzing their one pink word
over and over, and the road kept unspooling like it knew where it was going even
if we didn't, and that was the thing, that was always the thing.`,
  bridge: `There is no sane reason to take a motorcycle across the Bay Bridge at three in the
morning, which is exactly why it must be done. The city hangs out there in the fog
like a rumor of itself, all white towers and bad debts, and the bike is screaming
under you in fifth gear with the cables thrumming overhead like the strings of some
huge demented harp.`,
};

export function initTTS(send) {
  const input = document.querySelector("#speech-input");
  const count = document.querySelector("#speech-count");
  const speak = document.querySelector("#speech-speak");
  const clear = document.querySelector("#speech-clear");
  const stop = document.querySelector("#speech-stop");
  const status = document.querySelector("#speech-status");
  const history = document.querySelector("#speech-history");
  const player = new AudioPlayer();
  let live = false;
  let entries = [];
  const stream = createTtsStream({
    player,
    onActiveChange() {
      enabled();
    },
    onEvent(message) {
      if (message.type === "tts-start") {
        status.textContent = `synthesizing sentence 1/${message.sentences}`;
      } else if (message.type === "tts-status" && message.phase === "synthesizing") {
        status.textContent = `${player.chunks.length ? "playing · " : ""}synthesizing sentence ${message.sentence}/${message.sentences}`;
      } else if (message.type === "tts-done") {
        status.textContent = `playing (${Number(message.rtf).toFixed(2)}× real time)`;
      } else if (message.type === "tts-finished") {
        status.textContent = `done (${Number(message.audioSec).toFixed(1)} s audio)`;
      } else if (message.type === "tts-error") {
        status.textContent = `error: ${message.error}`;
      }
    },
  });

  function updateCount() {
    count.textContent = `${[...input.value].length} / ${MAX_LEN}`;
  }

  function enabled() {
    const busy = stream.active !== null;
    speak.disabled = !live || busy || !input.value.trim();
    input.disabled = busy;
    clear.disabled = busy;
    stop.hidden = !busy;
    for (const button of history.querySelectorAll("button")) {
      button.disabled = !live || busy;
    }
  }
  input.addEventListener("input", () => {
    updateCount();
    enabled();
  });
  clear.addEventListener("click", () => {
    input.value = "";
    updateCount();
    enabled();
    input.focus();
  });
  for (const button of document.querySelectorAll(".speech-seeds [data-seed]")) {
    button.addEventListener("click", () => {
      input.value = SEEDS[button.dataset.seed] ?? "";
      updateCount();
      enabled();
      input.focus();
    });
  }

  function request(text) {
    if (!text || !live || stream.active !== null) return;
    const id = crypto.randomUUID();
    if (send({ type: "tts-req", id, text })) {
      stream.begin(id);
      status.textContent = "sending…";
      enabled();
    }
  }
  speak.addEventListener("click", () => {
    request(input.value.trim());
  });
  stop.addEventListener("click", () => {
    if (stream.active) send({ type: "tts-stop", id: stream.active });
    void stream.reset();
    status.textContent = "stopped";
  });

  function renderHistory() {
    history.replaceChildren();
    if (!entries.length) {
      const empty = document.createElement("li");
      empty.className = "speech-history-empty";
      empty.textContent = "No speech generated yet.";
      history.append(empty);
      return;
    }
    for (const entry of entries) {
      const row = document.createElement("li");
      row.className = "speech-history-row";
      const time = document.createElement("time");
      time.dateTime = new Date(entry.ts).toISOString();
      time.textContent = new Date(entry.ts).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
      const origin = document.createElement("span");
      origin.className = "speech-origin";
      origin.textContent = entry.origin;
      const details = document.createElement("span");
      details.className = "speech-history-details";
      details.textContent = `${entry.chars} chars`;
      if (entry.cached) {
        const cached = document.createElement("span");
        cached.className = "speech-cached";
        cached.title = "Fully cached";
        cached.setAttribute("aria-label", "Fully cached");
        details.append(" ", cached);
      }
      const excerpt = document.createElement("span");
      excerpt.className = "speech-excerpt";
      excerpt.textContent = entry.text;
      const replay = document.createElement("button");
      replay.type = "button";
      replay.textContent = "Replay";
      // Replay always uses the sole baked voice; entries recorded under a
      // removed voice fall back silently.
      replay.addEventListener("click", () => request(entry.text));
      row.append(time, origin, details, excerpt, replay);
      history.append(row);
    }
    enabled();
  }

  return {
    status(connection) {
      live = connection === "live";
      if (live) {
        send({ type: "speech-log-req" });
      } else if (stream.active !== null) {
        // A dropped socket means tts-done may never arrive; recover Speak.
        void stream.reset();
        status.textContent = "connection lost";
      }
      enabled();
    },
    frame(message) {
      if (message.type === "speech-log") {
        entries = message.entries ?? [];
        renderHistory();
        return;
      }
      if (message.origin !== "console") return;
      stream.frame(message);
    },
  };
}
