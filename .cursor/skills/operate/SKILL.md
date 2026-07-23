---
name: operate
description: Install, run, and troubleshoot Virtual Me via the CLI (npx virtualme or ./cli.sh). Use when asked to start, stop, update, check, or diagnose a Virtual Me container.
---

# Operating Virtual Me

Virtual Me ships as a Docker image (`mayanklahiri/virtualme`) driven by a
zero-dependency Node CLI (`npx virtualme`, or `./cli.sh` from a checkout).

## Commands

| Command | Effect |
|---|---|
| `npx virtualme doctor` | Verify node/docker/daemon (+ git hooks in a checkout) |
| `npx virtualme start` | `docker run -d --name virtualme --restart unless-stopped --shm-size=1g -p 8080:8080 -v virtualme-data:/data mayanklahiri/virtualme:latest` |
| `npx virtualme status` | Container state + `/healthz` per-service report |
| `npx virtualme logs -f` | Follow container logs |
| `npx virtualme stop` | Stop and remove the container (volume survives) |
| `npx virtualme update` | Pull the latest image |
| `npx virtualme build` | Build `:dev` image from a source checkout |
| `npx virtualme keygen` | Print a 256-bit base64url token |

Env overrides: `VIRTUALME_IMAGE`, `VIRTUALME_TAG`.

## Endpoints (container running)

- `http://localhost:8080/` — embedded control-plane SPA
- `http://localhost:8080/healthz` — aggregate JSON health
- `ws://localhost:8080/ws` — server-push state snapshots
- `http://localhost:8080/desktop/` — proxied noVNC and websockify

The desktop UI opens `/desktop/vnc.html` with autoconnect, scaling, and the
proxied `/desktop/websockify` websocket path.

## Troubleshooting

1. `start` fails: run `doctor`; check `docker info`.
2. Unhealthy service: `virtualme logs` — s6 prefixes each line with the service name.
3. Slow first health: the ~3 GB Gemma model loads at startup; allow up to 5 minutes on a Raspberry Pi.
4. RAM: 8 GB minimum (Pi 5 or Pi 4 8GB). The LLM alone needs ~4 GB.
5. Trust model: prototype has NO auth/TLS — only run on a trusted private network.
