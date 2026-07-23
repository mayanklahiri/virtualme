import { AudioPlayer } from "./audio-player.js";

const MAX_LEN = 4096;

export function initTTS(send) {
  const input = document.querySelector("#speech-input");
  const count = document.querySelector("#speech-count");
  const speed = document.querySelector("#speech-speed");
  const speedValue = document.querySelector("#speech-speed-value");
  const speak = document.querySelector("#speech-speak");
  const stop = document.querySelector("#speech-stop");
  const status = document.querySelector("#speech-status");
  const player = new AudioPlayer();
  let active = null;
  let live = false;

  function enabled() {
    speak.disabled = !live || active !== null || !input.value.trim();
    input.disabled = active !== null;
    speed.disabled = active !== null;
    stop.hidden = active === null;
  }
  input.addEventListener("input", () => {
    count.textContent = `${input.value.length} / ${MAX_LEN}`;
    enabled();
  });
  speed.addEventListener("input", () => {
    speedValue.textContent = `${Number(speed.value).toFixed(1)}×`;
  });
  speak.addEventListener("click", () => {
    const text = input.value.trim();
    if (!text || !live || active !== null) return;
    const id = crypto.randomUUID();
    if (send({ type: "tts-req", id, text, speed: Number(speed.value) })) {
      active = id;
      status.textContent = "sending…";
      enabled();
    }
  });
  stop.addEventListener("click", () => {
    if (active) send({ type: "tts-stop", id: active });
    player.stop();
    active = null;
    status.textContent = "stopped";
    enabled();
  });

  return {
    status(connection) {
      live = connection === "live";
      enabled();
    },
    async frame(message) {
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
