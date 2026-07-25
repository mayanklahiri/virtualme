// Serialized TTS frame handling, DOM-free so Node tests can drive it.
//
// tts-* frames arrive on the websocket faster than AudioPlayer.begin()
// resolves; handling them concurrently silently dropped chunks (the player
// no-ops without a context) and could leave the active request id set
// forever. This module chains every frame through one promise queue and owns
// the active-request lifecycle: cleared when playback drains after tts-done,
// on tts-error, and on reset() (websocket reconnects).

/**
 * @typedef {{
 *   begin(sampleRate: number): Promise<void> | void,
 *   push(pcm: string): void,
 *   stop(): Promise<void> | void,
 *   remainingMS(): number,
 * }} PlayerLike
 */

/**
 * @param {{
 *   player: PlayerLike,
 *   onActiveChange?: (id: string | null) => void,
 *   onEvent?: (message: any) => void,
 *   schedule?: (fn: () => void, ms: number) => any,
 * }} options
 */
export function createTtsStream({
  player,
  onActiveChange = () => {},
  onEvent = () => {},
  schedule = (fn, ms) => setTimeout(fn, ms),
}) {
  /** @type {string | null} */
  let active = null;
  let queue = Promise.resolve();

  /** @param {string | null} id */
  function setActive(id) {
    if (active === id) return;
    active = id;
    onActiveChange(id);
  }

  /** @param {any} message */
  async function handle(message) {
    if (message.id !== active) return;
    if (message.type === "tts-start") {
      await player.begin(message.sampleRate);
      if (message.id !== active) {
        await player.stop();
        return;
      }
      onEvent(message);
    } else if (message.type === "tts-chunk") {
      player.push(message.pcm);
      onEvent(message);
    } else if (message.type === "tts-status") {
      onEvent(message);
    } else if (message.type === "tts-done") {
      onEvent(message);
      const id = message.id;
      schedule(() => {
        if (active !== id) return;
        setActive(null);
        onEvent({ type: "tts-finished", id, audioSec: message.audioSec });
      }, player.remainingMS());
    } else if (message.type === "tts-error") {
      await player.stop();
      setActive(null);
      onEvent(message);
    }
  }

  return {
    get active() {
      return active;
    },
    /** @param {string} id */
    begin(id) {
      setActive(id);
    },
    /** @param {any} message */
    frame(message) {
      queue = queue.then(() => handle(message)).catch(() => {});
      return queue;
    },
    async reset() {
      setActive(null);
      queue = Promise.resolve();
      await player.stop();
    },
  };
}
