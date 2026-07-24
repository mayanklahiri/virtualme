import { AudioPlayer } from "./audio-player.js";

const MAX_LEN = 4096;
const SEEDS = {
  if: `If you can keep your head when all about you
Are losing theirs and blaming it on you,
If you can trust yourself when all men doubt you,
But make allowance for their doubting too;
If you can wait and not be tired by waiting,
Or being lied about, don't deal in lies,
Or being hated, don't give way to hating,
And yet don't look too good, nor talk too wise.`,
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
  const voice = document.querySelector("#speech-voice");
  const speak = document.querySelector("#speech-speak");
  const clear = document.querySelector("#speech-clear");
  const stop = document.querySelector("#speech-stop");
  const status = document.querySelector("#speech-status");
  const history = document.querySelector("#speech-history");
  const player = new AudioPlayer();
  let active = null;
  let live = false;
  let entries = [];

  const storedVoice = localStorage.getItem("vm-voice");
  if ([...voice.options].some((option) => option.value === storedVoice)) {
    voice.value = storedVoice;
  }

  function updateCount() {
    count.textContent = `${[...input.value].length} / ${MAX_LEN}`;
  }

  function enabled() {
    speak.disabled = !live || active !== null || !input.value.trim();
    input.disabled = active !== null;
    voice.disabled = active !== null;
    clear.disabled = active !== null;
    stop.hidden = active === null;
    for (const button of history.querySelectorAll("button")) {
      button.disabled = !live || active !== null;
    }
  }
  input.addEventListener("input", () => {
    updateCount();
    enabled();
  });
  voice.addEventListener("change", () => {
    localStorage.setItem("vm-voice", voice.value);
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

  function request(text, selectedVoice) {
    if (!text || !live || active !== null) return;
    const id = crypto.randomUUID();
    if (send({ type: "tts-req", id, text, voice: selectedVoice })) {
      active = id;
      status.textContent = "sending…";
      enabled();
    }
  }
  speak.addEventListener("click", () => {
    request(input.value.trim(), voice.value);
  });
  stop.addEventListener("click", () => {
    if (active) send({ type: "tts-stop", id: active });
    player.stop();
    active = null;
    status.textContent = "stopped";
    enabled();
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
      const shortVoice = entry.voice === "en_GB-alba-medium" ? "Alba" : "Lessac";
      details.textContent = `${shortVoice} · ${entry.chars} chars`;
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
      replay.addEventListener("click", () => request(entry.text, entry.voice));
      row.append(time, origin, details, excerpt, replay);
      history.append(row);
    }
    enabled();
  }

  return {
    status(connection) {
      live = connection === "live";
      if (live) send({ type: "speech-log-req" });
      enabled();
    },
    async frame(message) {
      if (message.type === "speech-log") {
        entries = message.entries ?? [];
        renderHistory();
        return;
      }
      if (message.origin !== "console" || message.id !== active) return;
      if (message.type === "tts-start") {
        await player.begin(message.sampleRate);
        status.textContent = `synthesizing sentence 1/${message.sentences}`;
      } else if (message.type === "tts-chunk") {
        player.push(message.pcm);
      } else if (message.type === "tts-status" && message.phase === "synthesizing") {
        status.textContent = `${player.chunks.length ? "playing · " : ""}synthesizing sentence ${message.sentence}/${message.sentences}`;
      } else if (message.type === "tts-done") {
        status.textContent = `playing (${Number(message.rtf).toFixed(2)}× real time)`;
        setTimeout(() => {
          if (active !== message.id) return;
          status.textContent = `done (${Number(message.audioSec).toFixed(1)} s audio)`;
          active = null;
          enabled();
        }, player.remainingMS());
      } else if (message.type === "tts-error") {
        status.textContent = `error: ${message.error}`;
        player.stop();
        active = null;
        enabled();
      }
    },
  };
}
