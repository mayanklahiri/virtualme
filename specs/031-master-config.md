# Spec 031: Master Configuration Schema, Runtime Loader, and Console

| | |
|---|---|
| Status | Accepted (2026-07-25) |
| Depends on | `specs/003-controller.md`, `specs/007-persistence-locality.md`, `specs/009-local-tts.md`, `specs/010-outbound-mail.md`, `specs/021-agent-cdp-tools-console.md`, `specs/028-data-explorer.md` |
| Produces | One strongly typed, schema-documented master configuration; strict YAML loading and atomic persistence; migration of controller/ttsd/runtime-service knobs; secret references; `/config`; deterministic generated configuration reference |
| Followed by | `specs/032-assistant-notifications.md` (save-notification integration), `specs/033-telegram.md` (depends on this configuration foundation) |
| Consumed by | `specs/030-docs-site.md` consumes `docs/src/generated/config-reference.json`; 031 must generate that artifact even if 030 executes later |

## 0. Executor instructions

1. The constitution and all cited specs bind. Runtime Go and browser code remain
   stdlib-only; `controller/go.mod` must retain no `require` block. The JSON
   Schema validator and YAML parser/emitter are hand-written.
2. This is an implementation contract, not permission to redesign settings.
   Use the names, defaults, precedence, protocol, file layout, and ownership
   boundaries below exactly. Do not add a second configuration representation.
3. Execute test-first:
   1. add the failing Go, Node, and e2e tests in §12;
   2. run the narrow test and record that it fails for the intended missing
      behavior, not a fixture or syntax error;
   3. implement C1-C5 in order, stopping on every unrelated red test;
   4. run the complete deterministic offline gate and container e2e;
   5. perform the reconciliation pass in §13 last.
4. The core of 031 must execute before 032. Define the notification interface
   and no-op implementation now; do not import or anticipate 032 internals.
   Once 032 exists, its implementation supplies that interface and closes the
   conditional acceptance item.
5. `VM_DATA_DIR` is bootstrap configuration. It must remain a CLI/environment
   input because it locates `virtualme.config.yaml`; it is never a YAML field.
6. Preserve supervisor startup correctness. Controller-owned YAML cannot
   configure an already-started Xvfb, llama, Valkey, Chromium, noVNC, mail init,
   or `ttsd`. The preflight/config-export design in §8 is mandatory.
7. Existing installations with no file must start unchanged: seed the canonical
   default file first, then start services. Existing explicit legacy overrides
   remain effective during the deprecation window defined in §5.
8. Invalid present configuration is fatal before any long-running service
   starts. Never silently replace, ignore, or partially apply it.
9. The trust model remains unchanged: no auth or TLS. Secret redaction is a
   data-handling requirement, not an authentication boundary.
10. Finish only when every unchecked item in §14 has been manually evaluated.

## 1. Scope and invariants

This spec implements five inseparable capabilities:

- **C1 — schema and typed model:** an embedded, meta-validated JSON Schema is
  the source of defaults, docs, strict types, UI hints, restart ownership,
  legacy environment mappings, and secret policy.
- **C2 — complete migration:** controller, `ttsd`, and configurable supervised
  runtime services consume one effective configuration. Bootstrap,
  supervisor/container-only, and application runtime settings stay explicitly
  separated.
- **C3 — safe persistence:** a strict YAML subset, canonical commented emitter,
  secret-reference resolver, cache, redaction, and atomic `0600` writes.
- **C4 — console:** `/config` renders schema-driven views/forms and saves
  through exactly the startup validation pipeline, with a deliberate restart.
- **C5 — documentation export:** a deterministic, stale-checked JSON artifact
  under `docs/src/generated/` contains enough structured depth for a
  Stripe-quality reference.

Global invariants:

1. `controller/config/schema.json` is the only declaration of configurable
   fields. Go structs mirror it, but defaults and descriptions must not be
   copied into Go constants.
2. Unknown YAML keys, unknown schema keywords, unsupported YAML syntax,
   invalid legacy overrides, unresolved references, and semantic conflicts are
   errors. There is no permissive mode.
3. The raw persisted/API representation contains unresolved references.
   Effective internal configuration contains resolved secrets. Secret bytes
   never enter logs, websocket frames, HTTP responses, generated docs, error
   strings, or notification payloads.
4. Every process sees one precedence algorithm and one schema version.
5. Saving always rewrites the complete canonical file. Arbitrary user comments
   are intentionally not preserved; schema-derived comments are stable.
6. Configuration is restart-applied, never hot-reloaded. Save and restart are
   separate operations.

## 2. C1 — package and file layout

Add exactly this layout, adjusting only test-file partitioning when useful:

```text
controller/
├── internal/config/
│   ├── schema.json                    # source of truth; //go:embed schema.json
│   ├── embed.go                       # embedded-schema accessor
│   ├── types.go                       # Config and nested typed structs
│   ├── schema.go                      # schema decode/meta-validation/defaults
│   ├── validate.go                    # schema + semantic validation
│   ├── yaml.go                        # strict parser and canonical emitter
│   ├── secret.go                      # reference parsing/cache/redaction
│   ├── load.go                        # phases and precedence
│   ├── atomic.go                      # durable replacement
│   ├── export.go                      # UI/docs projections
│   └── *_test.go
├── internal/configapi/
│   ├── api.go                         # REST handlers and restart coordinator
│   └── api_test.go
├── cmd/configctl/
│   ├── main.go                        # preflight, service-env, docs export/check
│   └── main_test.go
├── cmd/controller/main.go
└── cmd/ttsd/main.go
```

The existing Go module permits every command to import
`github.com/mayanklahiri/virtualme/controller/internal/config`; no cross-module
or `main` import is needed. Build `/usr/local/bin/configctl` beside `controller`
and `ttsd` in the existing Docker builder.

`Config` has these top-level typed members and JSON names:

```go
type Config struct {
    Version  int
    System   SystemConfig
    Server   ServerConfig
    Desktop  DesktopConfig
    Valkey   ValkeyConfig
    Llama    LlamaConfig
    TTS      TTSConfig
    Agent    AgentConfig
    Mail     MailConfig
    Health   HealthConfig
    Integrations IntegrationsConfig
}
```

Every nested scalar is concrete (`string`, `int`, `int64`, `bool`); optional
mail values use strings whose default is empty. `RawConfig` remains a generic
tree solely for validation, canonical emission, and unresolved API output.
Decode into `Config` only after validation. Use `json.Decoder` with
`DisallowUnknownFields` for the final generic-tree-to-typed conversion as a
defense-in-depth assertion.

`IntegrationsConfig` is an empty, required, defaulted object in core spec 031
with the `vm-config-integrations-section` metadata and
`additionalProperties:false`. It is the explicit extension point for later
specs: spec 033 adds its typed `Telegram TelegramConfig` property to both the
schema and Go struct. Until then it emits as `integrations: {}` and the Config
UI may omit the empty section from its read view.

## 3. JSON Schema contract and validator subset

### 3.1 Dialect and root

`schema.json` is valid JSON, declares
`"$schema":"https://json-schema.org/draft/2020-12/schema"`,
`"$id":"https://virtualme.local/schemas/config-v1.json"`, title, description,
`type:"object"`, `additionalProperties:false`, and requires every top-level
section plus `version`. `version` is integer `const:1`, default `1`.

Objects use explicit `required` and `additionalProperties:false`. Every leaf
has `type`, `default`, `description`, `x-vm-doc`, `x-vm-ui`,
`x-vm-restart`, and, when applicable, `x-vm-env`, `x-vm-secret`, and
`x-vm-sensitive`. Section objects carry `x-vm-doc` and `x-vm-ui` too.

The hand-written validator supports exactly:

- types `object`, `array`, `string`, `integer`, `number`, `boolean`, and
  `null`; one type string only, not unions;
- `properties`, `required`, `additionalProperties:false`;
- `enum`, `const`, `minimum`, `maximum`, `minLength`, `maxLength`, `pattern`;
- homogeneous `items` and `uniqueItems:true` (the only accepted
  `uniqueItems` value), with deep JSON equality after scalar interpolation;
- `default`, `title`, `description`;
- local `#/$defs/...` references via `$defs` and `$ref`;
- the `x-vm-*` extensions in §4.

Reject every other schema keyword during schema initialization. References
must be local, acyclic, resolve exactly once, and may target only `$defs`.
Patterns use Go RE2 through `regexp`; schema startup fails if one does not
compile. Number validation rejects NaN/infinity. An integer JSON number must
round-trip as an integer and fit signed 64 bits.

Schema initialization runs once per process and returns an error rather than
panicking. Tests meta-validate the schema using the same code path used by
controller, `ttsd`, and `configctl`.

### 3.2 Defaults

Defaults are applied recursively to a newly allocated tree before YAML overlay.
Every property has a default, including empty objects/arrays where applicable;
therefore the resulting typed `Config` is complete. A default must validate
against its own subschema. Object defaults are deep-copied; no caller may
mutate schema storage.

The first-start file is the canonical emitter output of this default tree, not
a separately maintained template or fixture. A byte-for-byte golden locks it.

## 4. Vendor extensions

The validator meta-validates extensions rather than merely passing them
through.

### 4.1 `x-vm-doc`

Required on every section and leaf:

```json
{
  "overview": "One-sentence purpose.",
  "details": ["Ordered concrete behavior paragraphs."],
  "tradeoffs": [{"choice":"Value or strategy","benefit":"...","cost":"..."}],
  "examples": [{"label":"Local CPU","yaml":"llama:\n  threads: 8\n"}],
  "links": [{"label":"Spec 018","href":"../../specs/018-gpu-observability.md"}],
  "order": 10
}
```

`overview` is non-empty string; `details`, `tradeoffs`, `examples`, and
`links` are arrays (empty is allowed only when genuinely inapplicable);
objects reject extra keys; `order` is a unique non-negative integer among
siblings. Links are repository-relative or `https://`, never `javascript:`.
Example YAML must parse with the production subset parser and validate as a
partial overlay after merging defaults.

### 4.2 `x-vm-ui`

Required leaf shape:

```json
{
  "section": "Inference",
  "component": "vm-number-field",
  "control": "number",
  "order": 30,
  "advanced": false
}
```

`section` is non-empty; `component` is one of
`vm-text-field`, `vm-number-field`, `vm-checkbox`, `vm-select`,
`vm-secret-reference`, `vm-path-field`, `vm-address-field`, or
`vm-readonly-field`, or `vm-string-list`; `control` is one of `text`,
`number`, `checkbox`, `select`, `secret`, `path`, `address`, `readonly`, or
`string-list`; `order` is unique among
siblings; `advanced` is boolean. Type/control compatibility is meta-validated.
The SPA dispatches these names through a fixed control-renderer map and errors
visibly on an unknown component; it does not infer a different control.

Each top-level section object instead uses:

```json
{
  "section": "Inference",
  "sectionRenderer": "vm-config-inference-section",
  "order": 50,
  "advanced": false
}
```

`sectionRenderer` is one of `vm-config-system-section`,
`vm-config-network-section`, `vm-config-service-section`,
`vm-config-inference-section`, `vm-config-agent-section`,
`vm-config-mail-section`, `vm-config-health-section`, or
`vm-config-integrations-section`. The Config read view dispatches each section
through this fixed registry so inference can emphasize context/model resources,
mail can group identity/relay/DKIM, integrations can show external-boundary
warnings, and ordinary sections retain their purpose-built labelled layout.
Edit controls inside each section still come exclusively from leaf
`component`. Unknown section renderers are schema initialization errors, not a
generic silent fallback.

`vm-string-list` is valid only for an array whose `items.type` is `string`.
It renders ordered repeatable text rows with Add/Remove controls, preserves
array order, validates each item with the item schema, and reports duplicate
rows when `uniqueItems:true`. It never accepts comma-separated coercion.

### 4.3 Other extensions

- `x-vm-secret`: absent/false for ordinary fields, or an object
  `{"cacheTtlSeconds":300,"allowEnv":true,"allowFile":true}`. It is legal only
  on strings, requires `vm-secret-reference`, and has TTL range 0-86400. It
  may additionally carry
  `"resolveWhen":{"path":"integrations.telegram.enabled","equals":true}`.
  `resolveWhen.path` must resolve to a boolean schema leaf and may not form a
  cycle; `equals` is boolean. When the condition is false, validate and retain
  the unresolved reference but do not read its environment variable/file.
- `x-vm-restart`: required enum value `none`, `controller`, `ttsd`, `llama`,
  `desktop`, `valkey`, `mail`, or `all`. `none` is limited to display-only
  metadata. Saving computes the affected service closure in §11.
- `x-vm-env`: optional non-empty legacy environment name matching
  `^[A-Z][A-Z0-9_]*$`; names are globally unique except the deliberate
  `VM_XDOTOOL` alias on `agent.xdotoolPath` and `health.xdotoolPath`.
- `x-vm-sensitive`: optional enum `reference`, `credential`, or `path`.
  Secret fields require `credential`; public API projections apply the
  redaction rules in §9. Paths may be shown unless also secret.
- `x-vm-integration`: optional only on an object below `integrations`, with
  exact shape
  `{"external":true|false,"egressHosts":["https://host"],"capabilities":["lowercase-id"]}`.
  Reject extra keys, duplicate/syntactically invalid hosts, non-HTTPS hosts
  when `external:true`, and capability IDs outside
  `^[a-z][a-z0-9-]{0,47}$`. This metadata feeds Config/docs privacy warnings;
  it grants no networking permission by itself.

Tests deliberately mutate each extension shape and assert a path-specific
meta-validation failure.

## 5. C2 — ownership, precedence, and migration

### 5.1 Exact precedence

There are two layers:

1. **Bootstrap:** CLI `--data` > host `VIRTUALME_DATA` > host default
   `~/.virtualme`; the CLI passes the mounted in-container location as
   `VM_DATA_DIR`. A directly run binary uses non-empty `VM_DATA_DIR`, else
   `/home/virtualme/.virtualme`. This value exclusively selects
   `$VM_DATA_DIR/virtualme.config.yaml` and derives persistent paths. YAML
   cannot override it.
2. **Each schema field:** a non-empty explicitly present legacy environment
   variable named by `x-vm-env` wins; otherwise an explicitly present YAML
   value wins; otherwise the schema default wins. Empty environment strings
   count as explicit only for string fields whose schema allows empty; for
   numeric/boolean fields empty is invalid. Parse legacy booleans as exactly
   `0|1|false|true`, case-insensitive. Invalid overrides are fatal and name the
   variable and target config path.

After precedence, whole-scalar environment interpolation resolves for any
scalar leaf; secret environment/file references resolve under the stronger
rules in §9. Semantic validation runs both before resolution where possible
and after resolution without exposing secret values. No process may call
`os.Getenv` for migrated settings outside `internal/config`.

The deprecation window lasts through the next two minor releases after 031 is
executed. Every used legacy override logs once:
`config: legacy VM_FOO overrides path.to.field; migrate it to virtualme.config.yaml`.
Never log its value. Removal requires a later spec and release-note notice.

### 5.2 Bootstrap, supervisor, and container-only classification

| Class | Inputs | Rule |
|---|---|---|
| Bootstrap | `--data`, `VIRTUALME_DATA`, `VM_DATA_DIR` | Retained outside YAML; required to locate persistent root |
| Runtime master config | Every field in §6 | Owned by schema/YAML; exported before supervised services start and loaded directly by Go processes |
| Supervisor/container-only | `VM_LLAMA_GPU`, `NVIDIA_DRIVER_CAPABILITIES`, Docker `--gpus`, `VM_CHROMIUM_NO_SANDBOX`, `S6_*`, `HOME`, `XDG_*`, `LANG`, mount/port/tmpfs/user settings | Not claimed by controller; CLI/image/s6 retain ownership |

`VM_CHROMIUM_NO_SANDBOX` is intentionally container-only because it describes
host namespace capability and must be decided before Chromium. `VM_LLAMA_GPU`
and NVIDIA variables remain CLI hardware-injection signals. The master config
still owns llama model, context, threads, and endpoints. `XDG_*` remain derived
from bootstrap data root. `TZ` migrates to `system.timezone`; preflight exports
it to all services and Go code uses the typed value, while legacy `TZ` remains
its temporary override.

## 6. Exact v1 configuration tree

All addresses are loopback unless `server.httpAddress`, whose default
intentionally exposes the mapped console port. Semantic validation enforces
loopback for internal service addresses/URLs.

| YAML path | Type / constraints | Default | Legacy env | Restart |
|---|---|---|---|---|
| `version` | integer const 1 | `1` | — | all |
| `system.timezone` | IANA zone string, 1-128 | `UTC` | `TZ` | all |
| `server.httpAddress` | address string | `:8080` | `VM_HTTP_ADDR` | controller |
| `server.desktopProxyURL` | loopback HTTP URL | `http://127.0.0.1:6080` | — | controller |
| `desktop.display` | `^:[0-9]+$` | `:99` | `VM_DISPLAY` | desktop |
| `desktop.resolution` | `^[0-9]+x[0-9]+x(16|24|32)$` | `1600x900x24` | `VM_RESOLUTION` | desktop |
| `desktop.x11SocketDirectory` | absolute path | `/tmp/.X11-unix` | `VM_X11_SOCKET_DIR` | desktop |
| `desktop.vncAddress` | loopback host:port | `127.0.0.1:5900` | `VM_VNC_ADDR` | desktop |
| `desktop.noVNCAddress` | loopback host:port | `127.0.0.1:6080` | — | desktop |
| `desktop.noVNCUpstreamAddress` | loopback host:port | `127.0.0.1:5900` | — | desktop |
| `desktop.noVNCHealthURL` | loopback HTTP URL | `http://127.0.0.1:6080/vnc.html` | `VM_NOVNC_URL` | controller |
| `desktop.cdpURL` | loopback HTTP URL | `http://127.0.0.1:9222` | — | desktop |
| `desktop.chromiumWatchdogGrace` | integer 1-30 | `3` | `VM_CHROMIUM_WATCHDOG_GRACE` | desktop |
| `valkey.address` | loopback host:port | `127.0.0.1:6379` | `VM_VALKEY_ADDR` | valkey |
| `llama.address` | loopback host:port | `127.0.0.1:8081` | `VM_LLAMA_PORT` adapter | llama |
| `llama.contextTokens` | integer 2048-131072 | `32768` | `VM_LLAMA_CTX` | llama |
| `llama.modelPath` | absolute path | `/opt/models/gemma-4-E2B-it-Q4_0.gguf` | `VM_MODEL_PATH` | llama |
| `llama.projectorPath` | absolute path | `/opt/models/mmproj-gemma-4-E2B-F16.gguf` | `VM_MMPROJ_PATH` | llama |
| `llama.threads` | integer 0-1024; 0 means `nproc` | `0` | `VM_THREADS` | llama |
| `llama.chatCompletionsURL` | loopback HTTP URL | `http://127.0.0.1:8081/v1/chat/completions` | — | controller |
| `tts.address` | loopback host:port | `127.0.0.1:8082` | `VM_TTS_PORT` adapter | ttsd |
| `tts.healthURL` | loopback HTTP URL | `http://127.0.0.1:8082/healthz` | `VM_TTS_HEALTH_URL` | controller |
| `tts.sherpaDirectory` | absolute path | `/opt/sherpa-onnx` | `VM_SHERPA_DIR` | ttsd |
| `tts.modelDirectory` | absolute path | `/opt/models/tts` | `VM_TTS_MODEL_DIR` | ttsd |
| `tts.cacheDirectory` | absolute path or data-root token | `${data}/tts-cache` | `VM_TTS_CACHE_DIR` | ttsd |
| `tts.cacheMaxMiB` | integer 1-65536 | `256` | `VM_TTS_CACHE_MAX_MB` | ttsd |
| `tts.maxCharacters` | integer 1-65536 | `4096` | `VM_TTS_MAX_CHARS` | ttsd |
| `agent.maxSteps` | integer 1-5000 | `500` | `VM_AGENT_MAX_STEPS` | controller |
| `agent.keepTasks` | integer 1-1000 | `20` | `VM_AGENT_KEEP_TASKS` | controller |
| `agent.xdotoolPath` | absolute path or executable name | `xdotool` | `VM_XDOTOOL` | controller |
| `agent.scrotPath` | absolute path or executable name | `scrot` | — | controller |
| `agent.convertPath` | absolute path or executable name | `convert` | — | controller |
| `agent.bashPath` | absolute path or executable name | `bash` | — | controller |
| `agent.systemManifestPath` | absolute path | `/opt/agent/system-manifest.json` | — | controller |
| `health.llamaURL` | loopback HTTP URL | `http://127.0.0.1:8081/health` | `VM_LLAMA_HEALTH_URL` | controller |
| `health.xdotoolPath` | executable/path | `xdotool` | `VM_XDOTOOL` alias | controller |
| `mail.mailname` | string 0-255; empty means hostname | `""` | `VM_MAIL_MAILNAME` | mail |
| `mail.from` | string 0-320 | `""` | `VM_MAIL_FROM` | controller |
| `mail.smarthost.host` | hostname, empty disables relay | `""` | `VM_MAIL_SMARTHOST` | mail |
| `mail.smarthost.port` | integer 1-65535 | `587` | `VM_MAIL_SMARTHOST_PORT` | mail |
| `mail.smarthost.username` | string 0-512 | `""` | `VM_MAIL_SMARTHOST_USER` | mail |
| `mail.smarthost.password` | secret-reference string or empty | `""` | `VM_MAIL_SMARTHOST_PASS` adapter | mail |
| `mail.dkimDomain` | hostname or empty | `""` | `VM_MAIL_DKIM_DOMAIN` | controller |
| `mail.dkimSelector` | DNS label | `virtualme` | `VM_MAIL_DKIM_SELECTOR` | controller |
| `mail.flushSeconds` | integer 5-86400 | `60` | `VM_MAIL_FLUSH_SEC` | mail |
| `mail.sendmailPath` | absolute path | `/usr/sbin/sendmail` | `VM_SENDMAIL_PATH` | controller |
| `mail.spoolDirectory` | absolute/data-root token | `${data}/mail/spool` | `VM_MAIL_SPOOL_DIR` | mail |

`${data}` above is a schema default token expanded only by the loader from the
bootstrap root; it is not user interpolation and is accepted only as the
leading segment of those two documented path defaults.

The `VM_LLAMA_PORT` and `VM_TTS_PORT` legacy adapters accept a decimal port and
replace only the port in their corresponding default loopback address. They
also derive dependent URLs only when those URL fields were not explicitly set
by YAML or a higher-precedence legacy URL. `VM_XDOTOOL` maps identically to
both agent and health paths and is the sole permitted duplicate legacy mapping.
`VM_MAIL_SMARTHOST_PASS` is accepted as a literal only in the deprecation
adapter, held in memory, never persisted or returned, and emits a stronger
warning to migrate to `${env:...}` or `${file:...}`.

Semantic validation additionally requires:

- resolution width/height each 320-16384;
- all internal endpoints loopback and URL path appropriate to its role;
- no internal port collision except VNC upstream equals VNC address;
- `desktop.noVNCUpstreamAddress == desktop.vncAddress`;
- `server.desktopProxyURL` has the same authority as
  `desktop.noVNCAddress`;
- chat/health URLs have the same authority as `llama.address`; TTS health has
  the same authority as `tts.address`;
- smarthost username and password are either both empty or both present, and
  require a non-empty host;
- every absolute path is clean; executable names contain no slash unless
  absolute; no NUL/control characters;
- IANA timezone loads through `time.LoadLocation`.

These checks return ordered errors by YAML path.

## 7. C3 — strict YAML parser and canonical emitter

### 7.1 Accepted subset

The parser accepts UTF-8 (optional initial BOM), LF or CRLF, blank lines,
`#` comments outside quoted strings, nested mappings, block sequences, and
2-space indentation. Scalars are:

- double-quoted JSON-style strings;
- single-quoted strings using doubled single quotes;
- plain `null`, `true`, `false`;
- base-10 integers and finite decimal numbers without exponent notation;
- plain strings not colliding with those tokens.

Mapping keys must be plain schema identifiers matching
`^[A-Za-z][A-Za-z0-9]*$`. A sequence item is `- <scalar>` or `-` followed by a
nested mapping/sequence. Empty mappings/arrays are emitted as `{}`/`[]`; those
two exact tokens are the only permitted flow forms.

Reject tabs anywhere, anchors, aliases, tags, merge key `<<`, directives,
block scalars, general flow forms, explicit keys, multi-document markers,
duplicate keys, trailing garbage, odd indentation, indentation jumps,
unclosed quotes, invalid escapes, invalid UTF-8, and files over 1 MiB.
Record one-based line and column plus raw-config path on every parsed node.

Unknown fields are schema errors after parsing. A tailored error has:

```text
config /data/virtualme.config.yaml:42:5 at mail.smarthost.port:
expected integer 1..65535, got string "fast"
hint: use `port: 587`; see /config#mail-smarthost-port
```

Never include a secret scalar in `got`; print `<secret reference>` or
`<redacted>`. Aggregate at most 20 ordered errors, then append the omitted
count. Startup exits non-zero before services; controller/ttsd direct startup
uses the same format.

### 7.2 Canonical emission and atomic write

Emitter order is `x-vm-ui.order`; indentation is exactly two spaces. Before
each section emit its overview/details as wrapped `#` lines (88-column target).
Before each leaf emit description, default, restart impact, choices, and
tradeoff summary. Emit all fields, including defaults. Strings use plain style
only when unambiguous and free of `#`, `: `, leading/trailing whitespace, and
reserved scalar spellings; otherwise use double quotes. Secret references are
quoted. Output ends with one newline.

Write with `umask 077` semantics:

1. ensure data root exists and is not a symlink;
2. create a random temp file in the same directory with `O_EXCL`, mode `0600`;
3. write all bytes, `Sync`, `Close`, and `Chmod(0600)`;
4. rename over `virtualme.config.yaml`;
5. open and `Sync` the parent directory where supported; tolerate only the
   documented `EINVAL`/`ENOTSUP` directory-sync cases;
6. remove temp files on every failure.

Refuse a destination that is a symlink or non-regular file. Existing files are
normalized to `0600` after a successful replacement. Tests inject filesystem
operations to prove old bytes survive write, sync, and rename failures.

## 8. Loader phases and supervisor startup

`config.Load(Options)` is the sole pipeline:

1. initialize and meta-validate embedded schema;
2. derive root/file from bootstrap input;
3. if absent, lock `<file>.lock` with `O_CREATE|O_EXCL`, recheck, and atomically
   seed canonical defaults; stale locks are never guessed—failure reports the
   lock and exits;
4. read and parse raw YAML with locations;
5. overlay raw YAML on deep defaults;
6. apply typed legacy environment overrides;
7. validate unknown keys and interpolation placement/syntax, treating a valid
   whole-scalar placeholder as a deferred value at that leaf;
8. expand `${data}` defaults, resolve ordinary `${env:NAME}` placeholders to
   the target schema scalar type, and resolve secret `${env:...}`/
   `${file:...}` references through the injected resolver;
9. fully schema-validate the resolved effective tree;
10. run semantic checks;
11. decode typed config and calculate canonical unresolved-raw SHA-256.

The returned `Loaded` contains typed effective config, unresolved normalized
raw tree, source metadata per leaf (`default|yaml|legacy-env`), raw hash, and
redacted secret status.

Add `docker/rootfs/etc/cont-init.d/15-config.sh`, ordered after
`10-data-dirs.sh` has created the persistent lanes and before mail init. It
runs:

```sh
configctl preflight --data-dir "$VM_DATA_DIR" \
  --service-env /run/virtualme/config.env \
  --mail-dir "$VM_DATA_DIR/mail"
```

`preflight` loads once, writes a root-independent, shell-quoted, non-secret
service environment file mode `0600`, and writes dma configuration/auth files
atomically under the mail directory. It may put resolved mail credentials only
in `auth.conf` mode `0600`, never `config.env`. Existing
`20-mail-config.sh` becomes a verification/no-op or is removed; there is one
renderer.

Every shell service sources `/run/virtualme/config.env`. It contains derived
names scoped to scripts (`VM_EFFECTIVE_DISPLAY`, resolution, addresses,
model/projector paths, llama context/threads, watchdog grace, mail flush, and
`TZ`), not the deprecated names. Run scripts split validated host:port values
without `eval`. Xvfb, Openbox, x11vnc, noVNC, Chromium, watchdog, Valkey,
llama, and mailq use these values. Hardcoded CDP, llama, noVNC, VNC, and Valkey
endpoints are removed where represented in §6.

Controller and `ttsd` independently call `config.Load` at process start using
the same file and environment snapshot. This is intentional: s6 can start
them independently, and each must fail rather than inherit stale derived
values. `ttsd` receives the complete `TTSConfig`; controller receives the
complete `Config`. `health.FromEnv`, controller `envOr`/`envInt`,
`envResolution`, and state `os.Getenv("TZ"/"VM_DATA_DIR")` disappear in favor
of injected typed values. Persistent subpaths derive from bootstrap root.

`15-config.sh` failure causes s6 stage-2 failure under the existing policy;
no longrun starts. A service restart after a save rereads the same file. No
service watches the file.

## 9. Environment interpolation, secret references, cache, and redaction

`${env:NAME}` is accepted as an entire scalar at any scalar schema leaf.
`${file:/absolute/path}` is accepted only at a string leaf carrying
`x-vm-secret`. Exact syntax is:

- `${env:NAME}`, where `NAME` matches `^[A-Z][A-Z0-9_]*$`;
- `${file:/absolute/path}`, where the path is clean and absolute.

No embedded interpolation, escaping, nesting, relative file, or user-authored
`${data}` is permitted. Arrays and objects cannot be replaced by a reference.
After resolving a non-secret environment reference, convert its bytes according
to the target schema type before validation:

- string: exact environment bytes;
- integer: canonical base-10 `-?(0|[1-9][0-9]*)`;
- number: the accepted finite YAML decimal grammar, without exponent;
- boolean: exact lowercase `true` or `false`;
- null: exact lowercase `null`.

An absent environment variable is fatal and identifies its name and config
path without inventing a fallback. An empty environment value is valid only
for a target string whose schema accepts empty. The unresolved `${env:...}`
text remains in the raw tree and canonical YAML; only the effective typed tree
contains the converted value. Non-secret resolved values may be shown in the
Config read view, while the unresolved reference and source are shown beside
them.

Fields with `x-vm-secret` accept only empty string or one whole-scalar
`${env:...}`/`${file:...}` reference; a literal secret is invalid. The Config
UI's secret control accepts only those states and labels them explicitly.
Every ordinary scalar editor provides a `Literal`/`Environment reference`
mode; switching to the latter stores `${env:NAME}` after validating `NAME`.

Environment resolution snapshots `os.Environ()` once at loader creation;
refreshing does not observe later process environment mutation. File secrets:

- must be regular files, not symlinks, devices, sockets, or directories;
- maximum 64 KiB;
- mode must grant no group/other permission (`mode & 0077 == 0`);
- are read with no path cleaning surprise and trailing one CRLF/LF is removed;
- empty post-trim content is invalid.

The process-local cache key is the unresolved reference, never secret bytes.
Each schema field's `cacheTtlSeconds` controls expiry; v1 uses 300 seconds.
Concurrent misses coalesce. `Refresh(reference)` invalidates and rereads file
references; env references return their original snapshot value. Refresh
failure retains no newly read value and reports a redacted error. Shutdown
zeroes owned byte slices best-effort and clears maps.

The resolver also exposes
`Subscribe(reference string, fn func(SecretRevision)) (unsubscribe func())`.
`SecretRevision` contains only a monotonically increasing process-local
revision number, success boolean, redacted error code, and timestamp—never
secret bytes. A successful explicit `Refresh` increments the revision and
notifies subscribers after replacing the cache; failure notifies with
`success:false` while retaining the prior good cached value until its normal
expiry. Callbacks run outside resolver locks and are serialized per reference.
Services re-resolve through the resolver after a successful revision rather
than receiving secret bytes in the callback.

Raw/API state for a secret is:

```json
{
  "reference": "${file:/run/secrets/smtp}",
  "configured": true,
  "resolved": true,
  "source": "file",
  "lastRefreshAt": "2026-07-25T20:00:00Z",
  "error": ""
}
```

Timestamps are omitted in deterministic docs/tests and present in live API.
Neither secret length, hash, prefix, suffix, nor bytes are exposed. Central
`config.Redact(error|string|tree)` is applied before all config logs/errors,
API errors, activity records, and notifications. Tests use sentinel
`DO_NOT_LEAK_031` and assert it is absent from every serialized surface.

When `resolveWhen` is false, the state is
`{"configured":true,"resolved":false,"status":"inactive"}` with the unresolved
reference returned separately in raw config; it is not an error. Changing the
condition requires the owning restart class unless a later integration spec
defines a safe live transition.

## 10. C4 — REST protocol and `/config` UI

### 10.1 HTTP API

Register these before the SPA catch-all:

| Method/path | Contract |
|---|---|
| `GET /api/config/schema` | `200` JSON UI projection: schema version/hash, ordered sections/properties, constraints, docs and vendor extensions; no resolved values |
| `GET /api/config` | `200` JSON `{raw,effective,sources,secrets,fileHash,startupHash,pendingRestart,restartServices}`; raw has unresolved references; effective contains resolved non-secret values but replaces every secret leaf with `null` |
| `PUT /api/config` | body `{baseHash,config}` ≤1 MiB; validate and atomically save; response below |
| `POST /api/config/restart` | body `{pendingHash}`; confirm hash and schedule restart plan |
| `POST /api/config/secrets/refresh` | body `{path}`; refresh one configured secret and return redacted status |

All other methods return `405`; JSON decoding disallows unknown fields and
trailing values. Errors are
`{"error":{"code":"config_invalid","message":"...","issues":[{"path","line","column","message","hint"}]}}`.
Issue values are redacted. `baseHash` implements optimistic concurrency:
mismatch returns `409 config_conflict` with current hash and does not write.

`PUT` accepts the complete raw object, not YAML text. It runs the exact
pipeline from schema validation through semantic/secret validation, then
canonical emission and atomic replacement. On success:

```json
{
  "ok": true,
  "fileHash": "<sha256>",
  "pendingRestart": true,
  "restartServices": ["llama","controller"]
}
```

The server broadcasts the same non-secret payload as
`{"type":"config-saved",...}` after the response is committed. It calls an
injected interface after durable write:

```go
type SaveNotice struct {
	ChangedKeys     []string
	RestartRequired bool
	Revision        string
}
type ConfigNotifier interface {
	Saved(context.Context, SaveNotice) error
}
type ConfigNotifierFunc func(context.Context, SaveNotice) error
```

`ConfigNotifierFunc` implements `Saved`; 031's default is a no-op. Spec 032
supplies the operator-notification closure from `main.go` without making
`configapi` import the notification package. Notification failure is logged
redacted and does not roll back the save.

### 10.2 Page behavior

Add sidebar route `/config` and source modules `config.js` plus DOM-free
`config-model.js`. The page:

1. deep-links each section and property as
   `/config#<section>-<kebab-property-path>`;
2. defaults to a custom read view grouped by `x-vm-ui.section`, ordered by
   extension order, showing current unresolved value, effective source,
   default, restart badge, overview/details, choices, examples, links, and
   tradeoffs;
3. enters Edit mode by generating controls solely from `x-vm-ui.component`;
4. has Save and Discard. Discard restores the last server snapshot. Save sends
   the full raw object plus `baseHash`, focuses the first issue on 400, and
   reloads on success;
5. renders advanced fields collapsed per section but keyboard-accessible;
6. never places resolved secret bytes in DOM, JS state, browser storage, URL,
   clipboard helpers, or diagnostics;
7. uses DOM construction/textContent only, no `innerHTML`;
8. shows a persistent “Restart to update” button only when pending.

The trust note states that anyone reaching this private-network console may
change operational configuration and restart services. No new permission or
auth layer is added.

## 11. Save, pending restart, and graceful restart

At controller startup, `startupHash` is the canonical unresolved file hash.
After a save, `pendingRestart` is `fileHash != startupHash`. Reverting exactly
to startup bytes clears it. The affected restart set is always computed by
comparing the controller's immutable startup raw tree to the newest saved raw
tree and collecting `x-vm-restart`. It is never computed only against the
immediately previous save: multiple saves before restart must preserve every
still-unapplied service change. Expand:

- `desktop` → `svc-xvfb`, `svc-openbox`, `svc-x11vnc`, `svc-novnc`,
  `svc-chromium`, `svc-chromium-watchdog`, then controller;
- `llama`, `ttsd`, `valkey`, `mail` → corresponding service(s), then controller;
- `controller` → controller only;
- `all` → every configurable service, then controller.

Deduplicate and order by existing s6 dependency order. `restartServices` uses
stable logical names, not filesystem paths.

The restart button opens a confirmation listing affected services and warning
that active chat/tool/TTS work disconnects. On confirm it POSTs the exact
pending hash. The handler returns `409` if file/hash changed or no restart is
pending. Before accepting, the injected restart coordinator runs the exact
equivalent of:

```text
/usr/local/bin/configctl preflight --data-dir <fixed bootstrap root> \
  --service-env /run/virtualme/config.env --mail-dir <root>/mail
```

with fixed validated arguments and a 10-second context. This atomically
regenerates the service environment and dma/auth files from the newly saved
configuration; otherwise `s6-svc -r` would restart services with stale
container-init output. Preflight failure returns `503 config_preflight_failed`,
leaves the current services/controller running and config pending, and emits
no restarting broadcast. After successful preflight and the spec 032 lifecycle
planner (when installed), the handler:

1. returns `202 {"ok":true,"restarting":true,"pendingHash":"..."}`;
2. flushes the response, broadcasts
   `{"type":"config-restarting","pendingHash":"...","services":[...]}`;
3. waits 250 ms in a goroutine so HTTP/WS output can drain;
4. stops accepting new jobs, cancels active initiator work, and calls
   `http.Server.Shutdown` with a 5-second context;
5. invokes the restart coordinator which executes validated fixed
   `s6-svc -r /run/service/<name>` arguments for non-controller services;
6. returns from `main` cleanly so s6 respawns `svc-controller`.

Never call `os.Exit` in a handler. The production coordinator accepts only the
precomputed enum-to-path map; no request text reaches a command. Unit tests use
an injected coordinator and clock.

During restart the client marks the socket expected-closed, retries the normal
websocket reconnect schedule, and polls `GET /api/config` after reconnect.
Success requires `startupHash == fileHash`, pending false, and the server
state/health stream resumed. After 60 seconds show a non-destructive error with
Retry; do not automatically issue a second restart. A failed subordinate
service restart is logged and surfaced after reconnect through health; the
controller still respawns and reports pending based on its newly loaded hash.

## 12. Tests — write failing tests first

### 12.1 Go unit and golden tests

- Schema meta-validation: supported keyword inventory; every object closes
  properties; defaults validate; required/default completeness; local refs;
  duplicate extension orders/env names; every malformed extension variant.
- Validator table tests for every supported type/keyword and rejection of
  unsupported keywords, bad refs, cycles, overflow, NaN, and bad patterns.
- Default golden: emitted first-start YAML byte-for-byte; parse→emit→parse
  equality; schema and typed Config stay structurally synchronized.
- YAML valid corpus: nested maps/sequences, comments, all scalar types,
  quoting/escaping, CRLF/BOM, empty containers.
- YAML invalid corpus: tabs, duplicate keys, anchors/aliases/tags/merge,
  directives/doc markers, block/flow syntax, malformed indentation/jumps,
  invalid UTF-8, bad escapes, oversized file, and tailored line/column/path.
- Precedence matrix per leaf: default, YAML, legacy, empty env, malformed env;
  port adapters and dependent URL derivation; warning once without values.
- General environment interpolation: every scalar target type, unresolved raw
  preservation, typed effective values, missing/empty variables, invalid
  target grammar, and rejection in arrays/objects or embedded strings.
- Semantic tables: loopback, ports/collisions, URLs, resolution, paths,
  timezone, mail credential pairing, and stable ordered errors.
- Secrets: exact whole-scalar grammar; literal rejection; env snapshot;
  regular-file/mode/size/symlink checks; newline trim; TTL hit/miss/expiry;
  concurrent coalescing; `resolveWhen` inactive/active behavior; refresh
  revisions, serialized subscription/unsubscribe callbacks, prior-good-value
  retention on refresh failure; sentinel non-leak across all errors/JSON.
- Atomic writer fault injection at create/write/sync/close/chmod/rename/dir
  sync; destination symlink; mode `0600`; concurrent first-start lock.
- Loader startup: absent seeds; valid loads; invalid present never rewrites;
  schema hash/raw hash deterministic; direct controller and `ttsd` failures.
- Export: deterministic JSON golden, deep-link uniqueness, exemplar validity,
  choices/default/tradeoff completeness, and stale-check failure.
- Config API: method/body limits, unknown fields, GET projections, no secret
  bytes, 400 issues, 409 hashes, same validation pipeline, atomic save,
  notifier seam, refresh, restart ordering, cumulative startup-to-file service
  plan across multiple saves, preflight regeneration/failure, 202-before-
  shutdown, and no request-derived command arguments.

### 12.2 Node UI tests

Add DOM-free tests for schema ordering, deep-link generation, renderer dispatch,
raw-object editing, numeric/boolean conversion, secret-reference validation,
advanced grouping, issue-to-control focus, optimistic-conflict handling,
Discard, restart confirmation/service list, expected reconnect, and 60-second
failure state. Static tests require `/config` route/nav/skeleton, forbid
`innerHTML`, and assert no secret-status field can be rendered as a value.

### 12.3 Container e2e

Extend `test/e2e.sh` with isolated data roots:

1. first start creates mode-`0600` commented
   `virtualme.config.yaml`; restart leaves bytes unchanged;
2. seed one non-default YAML value before start and prove controller plus the
   owning supervised service use it;
3. seed malformed, unknown-key, wrong-type, duplicate-key, and bad-secret
   files; each container fails before longruns and logs tailored location/path
   without sentinel bytes;
4. prove a legacy override wins and emits one deprecation warning;
5. GET schema/config, save a harmless controller field, observe
   `config-saved`, pending hash, and unchanged running value before restart;
6. restart through API, observe response/broadcast before disconnect, reconnect,
   new value, equal hashes, and healthy state;
7. save a llama/ttsd-owned field and assert the restart plan includes and
   actually respawns that service;
8. concurrent stale save returns 409 and preserves newer bytes;
9. save one llama change then one controller-only change before restart and
   prove the plan still includes llama; prove regenerated `/run` env contains
   both newest values before subordinate service restart;
10. force restart preflight failure and prove no service restarts, no
    restarting frame is sent, and pending config remains retryable;
11. `configctl docs --check` passes without starting controller.

All fixtures are local. `npm run check` remains deterministic and network-free.

## 13. C5 docs export and final reconciliation

`configctl docs --output docs/src/generated/config-reference.json` loads the
embedded schema only, never starts controller or reads live/user configuration.
Output is pretty JSON with two-space indentation, LF, final newline, stable
schema/UI order, and no generation timestamp or machine path. It contains:

```json
{
  "schemaVersion": 1,
  "schemaSha256": "...",
  "sections": [{
    "id": "llama",
    "title": "Inference",
    "anchor": "llama",
    "consoleDeepLink": "/config#llama",
    "overview": "...",
    "details": ["..."],
    "exemplarYaml": "llama:\n  contextTokens: 32768\n",
    "settings": [{
      "path": "llama.contextTokens",
      "anchor": "llama-context-tokens",
      "consoleDeepLink": "/config#llama-context-tokens",
      "type": "integer",
      "default": 32768,
      "required": true,
      "choices": [],
      "constraints": {"minimum":2048,"maximum":131072},
      "restart": "llama",
      "legacyEnv": "VM_LLAMA_CTX",
      "secret": false,
      "overview": "...",
      "details": ["..."],
      "tradeoffs": [],
      "examples": [],
      "links": []
    }]
  }]
}
```

Every section and setting has a unique stable docs anchor plus a separate
console deep link. Every section has a parseable, valid exemplar YAML assembled
from its schema examples. Enum choices include value plus human explanation
sourced from docs. Defaults, constraints, restart impact, deprecation mapping,
secret policy, tradeoffs, examples, and links are retained rather than
flattened to prose. During docs export, repository-relative `x-vm-doc.links`
are deterministically converted to absolute
`https://github.com/mayanklahiri/virtualme/blob/main/<path>` URLs; existing
`https://` links pass through. The live `/config` projection may retain
same-origin repository-relative paths only when they resolve to an actual
console/docs route; otherwise it uses the same GitHub URL.

`configctl docs --check --output ...` generates in memory and byte-compares the
committed artifact, printing the regeneration command and exiting 1 on drift.
Wire this check into `scripts/check.sh` after Go tests. Final docs assets live
only below `docs/`; do not generate a second reference under controller/web.

The final implementation phase is `/master-update`, after all code/e2e tests:

1. update spec 007 §1 persistence map with
   `$VM_DATA_DIR/virtualme.config.yaml` (master config, mode `0600`) and its
   transient same-directory lock/temp files; update known-root assertions;
2. update README configuration, precedence/deprecation, `/config`, REST route,
   secret-reference, restart, generated-reference, and spec tables;
3. update `develop` with config package/schema rules, configctl commands, test
   gate, and “new setting starts in schema” procedure;
4. update `operate` with first-start location, safe edits, validation errors,
   legacy migration, secret-file permissions, UI restart, and recovery;
5. update AGENTS/CLAUDE and screenshots for `/config`;
6. verify spec 030 consumes the generated artifact path and spec 033 declares
   its dependency on 031; do not edit unexecuted specs merely to speculate;
7. run `npm run check`, smoke/e2e as required by the skill, and inspect the
   final diff for duplicated defaults or remaining migrated `os.Getenv`.

## 14. Acceptance checklist

- [ ] Failing Go, Node, and e2e tests were authored and observed before
      implementation; failures were for intended missing behavior.
- [ ] Embedded schema is the sole field/default/docs/UI/env/restart source and
      meta-validates with no unsupported keywords or malformed extensions.
- [ ] Default first start produces deterministic commented pretty YAML at
      `$VM_DATA_DIR/virtualme.config.yaml`, mode `0600`.
- [ ] Present invalid YAML exits before longruns with tailored file,
      line/column, path, expected/actual, and hint; it is never rewritten.
- [ ] Parser rejects every forbidden YAML feature and emitter round-trips the
      accepted subset canonically.
- [ ] Atomic fault tests prove the previous file survives all pre-rename
      failures and no temp artifact remains.
- [ ] Every controller and `ttsd` `VM_*` knob is mapped; hardcoded CDP, llama,
      TTS, Valkey, VNC/noVNC, and health endpoints in scope are configured.
- [ ] Bootstrap, runtime-supervisor, and container-only variables match §5;
      `VM_DATA_DIR` remains outside YAML.
- [ ] Precedence and legacy adapters match §5 exactly, including warnings and
      invalid-override fatal behavior.
- [ ] Controller, `ttsd`, configctl preflight, and all affected s6 services use
      the same file/pipeline without startup-order races.
- [ ] Any scalar leaf accepts whole `${env:NAME}` interpolation with exact
      target-type conversion and unresolved canonical persistence; missing,
      malformed, embedded, array, and object uses fail helpfully.
- [ ] Secret fields accept only empty or whole
      `${env:NAME}`/`${file:/absolute/path}` references; file/mode/size/cache/
      refresh rules pass.
- [ ] Sentinel secret bytes appear in no log, API, websocket, error,
      notification, docs artifact, DOM, or browser storage.
- [ ] `/config` custom view and generated edit form honor exact `x-vm-ui`
      components, orders, advanced flags, docs, defaults, choices, examples,
      links, and tradeoffs.
- [ ] Save/Discard, optimistic hash conflict, same-pipeline validation,
      canonical atomic rewrite, and pending restart behave as specified.
- [ ] Restart response and broadcast flush before graceful controller shutdown;
      affected services restart in order, s6 respawns controller, and the UI
      reconnects with equal startup/file hashes.
- [ ] 031 runs independently with a no-op notifier; after 032 exists, config
      save emits its operator notification with no secret material.
- [ ] `docs/src/generated/config-reference.json` is deterministic and complete;
      stale-check fails on a schema mutation and passes after regeneration.
- [ ] Spec 007 persistence map and deterministic known-root gate include the
      master config and transient write artifacts.
- [ ] `npm run check`, `bash test/smoke.sh`, and `bash test/e2e.sh` pass offline
      where applicable.
- [ ] `/master-update` ran last and reconciled README, AGENTS, CLAUDE, skills,
      spec tables, endpoint tables, and `/config` screenshots.
- [ ] Final grep finds no direct reads of migrated environment variables outside
      `internal/config`, CLI bootstrap/container decisions, or deprecation tests.

## Amendments

### 2026-07-25 — Execution record

Spec 031 was implemented after spec 030 in C1-C5 order. Red-first evidence was
observed before implementation:

- Go config tests failed on the intentionally absent embedded schema, YAML
  parser, loader, redactor, and documentation exporter symbols.
- Node Config-page tests failed because `config-model.js` did not exist.
- Container assertions failed because the master file, preflight renderer, and
  Config API/UI did not exist.

The executed implementation adds the embedded schema and typed model, strict
YAML/default/precedence/secret pipeline, durable canonical persistence,
`configctl` preflight and docs export, runtime/s6 migration, Config REST/UI
surface, cumulative restart coordinator, no-op notification seam, generated
reference, persistence-map updates, and reconciled operator/developer
documentation. `controller/go.mod` remains free of `require` blocks. The
notification integration remains intentionally conditional on spec 032; core
031 uses the required no-op implementation.

Final verification passed:

- `go test -race ./internal/config ./internal/configapi`
- `npm run check`
- `bash test/smoke.sh`
- `bash test/e2e.sh` (including invalid-config startup failures, YAML and
  legacy service consumption, save/conflict/cumulative-plan behavior,
  preflight-failure safety, websocket restart sequencing, service respawn,
  convergence, and byte-stable restart)
- `./cli.sh docs build`
- `/master-update`, including refreshed `/config` and all console screenshots

Optional hardware/slow probes (`E2E_GPU=1`, `E2E_AGENT=1`, and
`E2E_JIGGLER=1`) were not enabled; their existing deterministic default skips
are not spec-031 acceptance blockers.

### 2026-07-25 — Console fix and taste sweep

1. **Read view.** Settings render as an indented YAML tree: intermediate path
   segments become group headers; each leaf shows name, effective value (or
   redacted secret reference), one-line overview, and restart badge only.
2. **Edit view.** The same tree layout; each leaf is its schema-driven
   control only. No Literal/Environment mode selector (env references are
   typed directly as `${env:NAME}`); no Advanced `<details>` collapse — all
   fields appear inline in tree order.
3. **Deprecated key.** `integrations.telegram.allowedUserIds` is hidden from
   the UI projection; see spec 033 amendment for Valkey migration.

### 2026-07-26 — Remove configuration-owned timezone

The `system` section and its sole `system.timezone` field are removed from the
schema, typed model, semantic validation, preflight export, generated YAML, and
Config console. Timezone is process-environment state only: the CLI continues
to forward an explicit host `TZ`, supervised services inherit it, and the
controller reports `TZ` or its process location when the variable is absent.

Strict loading intentionally rejects existing configuration files that retain
a `system` key. There is no migration or compatibility alias.

### 2026-07-26 — Data-directory-relative secret files

Secret fields additionally accept the whole-scalar form
`${file:${data}/relative/path}`. The resolver expands it beneath
`$VM_DATA_DIR`, rejects absolute, unclean, or parent-traversing relative paths,
and then applies the same descriptor safety as absolute file references:
`O_NOFOLLOW`, regular files only, no group/other permissions, a 64 KiB cap,
and one trailing newline trimmed.

The Config editor, generated reference, Telegram example, and operator skill
expose the same grammar. `${data}` remains unavailable for arbitrary
non-secret user-authored paths.
