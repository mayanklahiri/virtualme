# Spec 020: Speech Console Upgrades, TTS Disk Cache, Second Voice, and Audio Hygiene

| | |
|---|---|
| Status | Executed (2026-07-23) |
| Depends on | `specs/009-local-tts.md` (ttsd, Speech tab, speak tool), `specs/013-job-queue-scheduler.md` (`internal/valkey`, activity ledger via spec 015) |
| Produces | Speech-page seed texts (Kipling / Kerouac-style / Thompson-style), a Clear button, speed selector removed from the UI, a server-global Valkey-persisted log of generated speech with replay; an exact-string disk cache in the TTS engine under `$VM_DATA_DIR/tts-cache/`; a second Piper voice (`en_US-ryan-medium`) on the same sherpa-onnx runtime with a voice selector; elimination of stray playback audio artifacts (the "water droplet"), with explicitly **no** completion chime |
| Followed by | Future specs |

## 0. Executor instructions

- Constitution binds: no new runtimes (the second voice is a model for the already-pinned sherpa-onnx), layers append-only, artifacts pinned URL+sha256.
- The seed texts in §2 are verbatim content — copy them exactly. The Kipling stanzas are public domain (1910). The other two are original style pastiches written for this spec; do NOT substitute real Kerouac or Thompson passages (still in copyright).
- Stop-on-red; finish with §9 Acceptance.

## 1. Speech page control changes

`controller/web/static/index.html` (`[data-page="speech"]`) + `controller/web/static/js/tts.js`:

1. **Remove the speed selector**: delete the `#speech-speed` range input, its `<output>`, and the JS that reads it. `tts-req` no longer includes `speed` from the console (the server default 1.0 applies). The WS/HTTP/ttsd `speed` parameter itself is kept for API clients and the agent `speak` tool — clamping logic in `controller/internal/tts` is untouched.
2. **Clear button**: in `.speech-actions`, before Stop: `<button id="speech-clear" class="secondary" type="button"><svg class="icon"><use href="/icons.svg#i-trash-2"/></svg>Clear</button>` — empties the textarea, resets the counter, focuses the textarea. No confirmation (text is trivially recoverable via seeds/history).
3. **Character counter**: keep `#speech-count` as `N / 4096` (this replaces the confusing live-build `0 / 48096` artifact — max length is 4096 and the counter must show exactly that).
4. **Voice selector** (§5): `<label for="speech-voice">Voice</label><select id="speech-voice"><option value="en_US-lessac-medium">Lessac (en-US)</option><option value="en_US-ryan-medium">Ryan (en-US)</option></select>` replacing the static `Voice Lessac (en-US)` span. Persist choice in `localStorage` `vm-voice`.

## 2. Seed texts

Above the textarea add a seed row: `<div class="speech-seeds" role="group" aria-label="Seed texts"><span class="lookback-label">Seeds</span><button type="button" data-seed="if">Kipling — If</button><button type="button" data-seed="road">Road notes</button><button type="button" data-seed="bridge">Night bridge</button></div>`. Clicking a seed replaces the textarea content (no append) and updates the counter. Store the texts in `tts.js` as a `const SEEDS = {...}` with these exact values:

`if` — Rudyard Kipling, "If—" (1910, public domain), first and last stanzas:

```
If you can keep your head when all about you
Are losing theirs and blaming it on you,
If you can trust yourself when all men doubt you,
But make allowance for their doubting too;
If you can wait and not be tired by waiting,
Or being lied about, don't deal in lies,
Or being hated, don't give way to hating,
And yet don't look too good, nor talk too wise.

If you can fill the unforgiving minute
With sixty seconds' worth of distance run,
Yours is the Earth and everything that's in it,
And—which is more—you'll be a Man, my son!
```

`road` — original stream-of-thought pastiche (Kerouac-style, written for this spec):

```
And so we went, the two of us and the whole hum of the valley night going with us,
past the fruit stands shuttered and the neon vacancy signs buzzing their one pink word
over and over, and I thought about everybody asleep in all those rooms dreaming their
separate Americas, and the road kept unspooling like it knew where it was going even
if we didn't, and that was the thing, that was always the thing — you don't drive the
road, the road drives you, mile after holy mile, until the dawn comes up gray and
gorgeous over the flats and you remember you forgot to be tired.
```

`bridge` — original pastiche on San Francisco, the Bay Bridge, and motorcycles (Thompson-style, written for this spec):

```
There is no sane reason to take a motorcycle across the Bay Bridge at three in the
morning, which is exactly why it must be done. The city hangs out there in the fog
like a rumor of itself, all white towers and bad debts, and the bike is screaming
under you in fifth gear with the cables thrumming overhead like the strings of some
huge demented harp. Halfway across you stop being a person with obligations and
become a simple physics problem — velocity, wind, nerve — and the toll you pay on
the far side has nothing to do with money.
```

## 3. Speech history log (server-global, Valkey)

1. **Record**: every completed synthesis — console `tts-req`, agent `speak`, and HTTP `POST /v1/audio/speech` — appends one JSON entry to Valkey list `virtualme:speech:log` (`LPUSH` newest-first, `LTRIM` to 100), written by the controller at the single point where synthesis completes (the `tts.Client.Synthesize` call sites; put the recording helper in `controller/internal/tts/log.go` taking the valkey client):

```json
{"ts":1690000000000,"origin":"console"|"chat"|"api","voice":"en_US-ryan-medium","speed":1.0,
 "chars":312,"durationMs":2140,"cached":false,"text":"first 500 chars"}
```

2. **WS**: client `{"type":"speech-log-req"}` → sender-only `{"type":"speech-log","entries":[…newest 50…]}`. Broadcast the same frame after each new entry. Push on WS connect alongside the existing connect frames.
3. **UI**: below the speech card, a **History** card: one row per entry — local short time, origin chip, voice short name, char count, `cached` dot when true, text excerpt (2 lines, CSS `line-clamp`), and a **Replay** button per row. Replay sends a normal `tts-req` with the entry's full stored text and voice (replay is re-synthesis; with §4's cache this is instant for cached entries — do not store audio blobs in Valkey).
4. Persistence map amendment (spec 007 §1a Amendments): `virtualme:speech:log` — speech history — Valkey AOF.

## 4. TTS disk cache (exact string match)

In the controller speech engine (`controller/internal/tts/tts.go`, ttsd side — the `Service` that shells out to `sherpa-onnx-offline-tts`):

1. **Key**: `sha256(voice + "\x00" + strconv.FormatFloat(speed,…) + "\x00" + sentenceText)` — **per sentence**, matching the engine's existing sentence-split pipeline (whole-request caching would defeat the streaming-first-sentence latency win; per-sentence exact match also hits across different requests sharing sentences).
2. **Layout**: `$VM_TTS_CACHE_DIR` (default `$VM_DATA_DIR/tts-cache/`), files `<hex>.wav` written atomically (`os.CreateTemp` in-dir + `os.Rename`). On hit: read the WAV, skip the subprocess, stream chunks exactly as today; mark the NDJSON `done` event and the history entry `cached:true` when **all** sentences hit.
3. **LRU cap**: after each write, if the dir exceeds `VM_TTS_CACHE_MAX_MB` (default 256), delete oldest-`mtime` files until under budget (touch files on read hits so mtime is the LRU clock).
4. ttsd gets the data dir via a `VM_DATA_DIR`-derived default; `cont-init.d/10-data-dirs.sh` adds `tts-cache` to the created dirs. Persistence map amendment row: `$VM_DATA_DIR/tts-cache/` — synthesized-audio cache — safe to delete anytime.
5. Hermetic tests: hit/miss/atomic-write/LRU-eviction with a fake runner producing deterministic WAV bytes; corrupted cache file (truncated WAV) is treated as a miss and rewritten.

## 5. Second voice — layer for `en_US-ryan-medium`

1. New layer `docker/layers/017-tts-voice-ryan.sh` (next unused number at execution time): fetch `https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/vits-piper-en_US-ryan-medium.tar.bz2` (the sherpa-converted Piper bundle: `.onnx`, `tokens.txt`, `espeak-ng-data/`) to `/opt/models/tts/vits-piper-en_US-ryan-medium/`, following layer 014's exact structure. Pin procedure: download once at authoring time, compute `sha256sum`, hard-code it in the script (constitution rule 7); add the `COPY`+`RUN` pair at the END of the Dockerfile layer sequence.
2. `controller/internal/tts`: replace the single `Voice` constant with a whitelist `var Voices = []string{"en_US-lessac-medium", "en_US-ryan-medium"}` (default first). `Synthesize`/ttsd `/synthesize` accept `"voice"`; invalid values fall back to default with a logged warning. The sherpa model dir is derived per voice: `/opt/models/tts/vits-piper-<voice>/` (make `VM_TTS_MODEL_DIR` the parent default `/opt/models/tts` and join per request).
3. **Surfaces**: WS `tts-req` gains optional `voice`; `POST /v1/audio/speech` maps the OpenAI `voice` field through (unknown → default, matching current permissive behavior); the agent `speak` tool schema gains optional `voice` enum.
4. Health probe: the existing `tts` probe is unchanged (one healthy engine suffices); add a startup log line listing available voice dirs found.

## 6. Stray-audio elimination (the "water droplet") — and no chime

Repo ground truth: there are **no** sound assets and no `new Audio()`/oscillator anywhere; the only audio path is streamed TTS PCM through `controller/web/static/js/audio-player.js` (`AudioContext` + scheduled `AudioBufferSourceNode`s). Random short "droplet/click" sounds are characteristic of PCM scheduling artifacts: (a) a chunk scheduled with `start(t)` where `t` has already passed plays a clipped fragment; (b) hard chunk boundaries with non-zero sample offsets produce clicks; (c) an `AudioContext` resumed with stale queued buffers can emit a blip.

1. **Fixes in `audio-player.js`**:
   - Maintain a `nextStartTime` cursor; schedule each buffer at `max(nextStartTime, context.currentTime + 0.02)` and advance by exact buffer duration — never schedule in the past.
   - Wrap every source in a `GainNode` with a 5 ms linear fade-in and fade-out ramp (`gain.setValueCurveAtTime` or two `linearRampToValueAtTime` calls) — declick at every chunk boundary.
   - On `stop()`: cancel scheduled sources AND ramp the master gain to 0 over 10 ms before `disconnect()` (no pop on stop).
   - Close/recreate the `AudioContext` per playback session (`begin()`), never reuse a suspended context with stale state.
2. **Audit**: repo-wide check (add to the acceptance list) that no other `AudioContext`, `new Audio`, `<audio>`, or oscillator exists in `controller/web/static/`; if the live deployment's droplet came from an uncommitted experiment, this spec's committed tree is the reference.
3. **Explicit non-goal**: NO completion chime is added anywhere. Chat completion remains visually signalled only (input re-enables on `chat-done`). Any future notification sound requires a new spec.

## 7. Requirement mapping

| User bullet | Section |
|---|---|
| Seed texts (If / Kerouac / Thompson) | §2 |
| Clear button | §1.2 |
| Remove speed selector | §1.1 |
| Log of previously generated speech, global, persisted to Valkey | §3 |
| Disk cache, exact string matching | §4 |
| Another TTS voice, no new runtime | §5 |
| Water-droplet sound elimination, no chime | §6 |

## 8. Tests and docs

- Hermetic Go: cache tests (§4.5), voice whitelist/fallback, history recording caps.
- e2e: extend `tts-probe.mjs` — request the same short string twice; the second response's `done` event must carry `cached:true` and complete in <25% of the first duration; request with `voice:"en_US-ryan-medium"` returns audio (byte length > 0) distinct from the lessac render of the same text.
- Docs: `/master-update` — operate skill (two voices, seeds, history/replay, cache location + `VM_TTS_CACHE_MAX_MB`), develop skill (layer table row 017, voice whitelist location), README.

## 9. Acceptance checklist

- [x] `npm run check` green.
- [x] Speech page: seeds fill the editor verbatim; Clear empties; no speed control renders; counter reads `N / 4096`.
- [x] Speaking a seed with each voice sounds distinct; history lists both entries with correct origin/voice; Replay of a cached entry starts in under ~1 s.
- [x] `$VM_DATA_DIR/tts-cache/` populates; filling it past the cap evicts oldest files; deleting the dir entirely is harmless (recreated).
- [x] Agent `speak` and `/v1/audio/speech` both appear in the history with origins `chat`/`api`.
- [ ] Rapid Speak/Stop cycles and mid-stream Stop produce no clicks/pops/droplets (headphone test); the deterministic source audit confirms no non-TTS audio source in the SPA, but this execution environment has no audible output for the headphone check.
- [x] History and cache survive container restart.

## Execution notes

- The Ryan bundle was downloaded from the exact §5 URL and independently
  hashed as `c546af78b6395b4e7c4ce1ed899438b64426a362f5d4ec5fecd090ded9ad7505`.
- Hermetic Go tests cover exact cache hits/misses, atomic writes, corrupted-WAV
  replacement, LRU eviction, voice fallback/model paths, and the 100-entry
  history cap. Browser-free tests lock the verbatim seeds and sole declicked
  audio path.
- The full container e2e suite rendered both voices distinctly, measured the
  second exact request below 25% of the first, exercised the Ryan HTTP API,
  and verified speech history plus cache files across a stop/start cycle.
- The only unperformed acceptance action is subjective headphone listening;
  no host audio device is exposed in the execution environment.

## Amendments

### 2026-07-24 — Alba replaces Ryan; trimmed seed texts

Live listening feedback: the second voice should be a distinct-sounding
British English female voice rather than a second American male voice, and
the seed texts were about twice as long as needed for a quick audition.

1. **Voice swap.** `en_US-ryan-medium` is replaced everywhere by
   **`en_GB-alba-medium`** (Piper VITS, medium tier — comfortably real-time
   on the same hardware class as Lessac). Docker layer
   `docker/layers/017-tts-voice-ryan.sh` is replaced by
   `017-tts-voice-alba.sh`, fetching
   `vits-piper-en_GB-alba-medium.tar.bz2` from the sherpa-onnx `tts-models`
   release, pinned sha256
   `fcd45962906933eec4431d3688f7d74aaac8713c87c6717f91fd3b23463aa1a1`.
   Editing layer 017 in place (same number, new voice) is recorded here as
   the constitution rule 6 spec amendment; the layer's role (baked-in second
   TTS voice) is unchanged.
2. **Touchpoints.** `tts.Voices` whitelist, the speech page `<select>`
   (label "Alba (en-GB)"), the agent `speak` tool voice enum, the speech
   history short-name mapping, README/AGENTS.md wording, and all probes/tests
   (`tts-probe.mjs`, `speech-log-probe.mjs`, `speech-audio.test.js`, Go tts
   and controller tests) now reference alba. Cached Ryan renders simply
   become unused LRU entries and age out.
3. **Seed trim.** The three seed texts are cut to one stanza/paragraph each
   (Kipling first stanza; road and bridge first passages), roughly halving
   their length; the Node test asserts both the retained openings and the
   absence of the removed endings.

### 2026-07-24 — Serialized TTS frame handling (spec 026 P1, P2, C1)

`tts-*` frames were dispatched without awaiting while `tts-start` awaited
`AudioPlayer.begin()`, dropping chunks that arrived mid-await (cached
synthesis lost everything after the first sentence) and leaving the Speak
button wedged on the `active` id when `tts-done` never arrived. A DOM-free
`tts-stream.js` module now serializes every frame through a promise queue,
owns `active`, and clears it on done/error/reset (reset fires on reconnect);
both the Speech tab and chat bubbles route through it. `AudioPlayer.push`
resumes a suspended context. The no-chime invariant extends to markup:
`aria-live` regions (which some platforms voice with a notification sound
per agent step) are removed from the console.
