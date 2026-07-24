# Virtual Me

A private background agent that runs entirely on your machine: local LLM,
real browser on a virtual desktop, speech, and outbound mail. No cloud, no
accounts, no telemetry.

## Run it

    npx virtualme start

or with plain Docker:

    docker run -d --name virtualme -p 8080:8080 \
      -v ~/.virtualme:/home/virtualme/.virtualme \
      mayanklahiri/virtualme:latest

Then open http://localhost:8080 in a browser on the same network.

Your data (browser profile, chat history, projects, mail queue) lives in
`~/.virtualme` on the host and survives updates.

## Good to know

- Needs about 8 GB of RAM; the local model alone uses ~4 GB.
- With an NVIDIA GPU and the NVIDIA Container Toolkit installed, `npx virtualme start` passes the GPU through automatically and runs the model on it (`--no-gpu` opts out).
- First start is slow while the model loads. Give it a few minutes.
- Prototype trust model: no auth or TLS. Run it on a trusted private network only.

Source, docs, and issues: https://github.com/mayanklahiri/virtualme
