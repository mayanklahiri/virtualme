# Virtual Me

A private background agent with a local LLM, a real browser on a virtual
desktop, recurring projects, durable jobs and notifications, speech, outbound
mail, and an optional Telegram Bot API integration. Core inference and state
stay on your machine; enabling Telegram deliberately sends authorized chat
traffic through Telegram's cloud service.

## Run it

    npx virtualme start

or with plain Docker:

    docker run -d --name virtualme -p 8080:8080 \
      -v ~/.virtualme:/home/virtualme/.virtualme \
      mayanklahiri/virtualme:latest

Then open http://localhost:8080 in a browser on the same network.

Your data (browser profile, chat history, projects, configuration,
notifications, metrics, and mail queue) lives in `~/.virtualme` on the host
and survives updates. The console includes Config, Jobs, Data, Notifications,
and Telegram integration pages.

## Good to know

- Needs about 8 GB of RAM; the local model alone uses ~4 GB.
- With an NVIDIA GPU and the NVIDIA Container Toolkit installed, `npx virtualme start` passes the GPU through automatically and runs the model on it (`--no-gpu` opts out).
- First start is slow while the model loads. Give it a few minutes.
- Prototype trust model: no auth or TLS. Run it on a trusted private network only.

Source, docs, and issues: https://github.com/mayanklahiri/virtualme
