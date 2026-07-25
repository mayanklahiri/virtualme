import { createServer } from "node:http";
import { appendFileSync, readFileSync } from "node:fs";

const token = process.env.TELEGRAM_STUB_TOKEN || "obviously-fake-runtime-token";
const record = process.env.TELEGRAM_STUB_RECORD || "";
const script = process.env.TELEGRAM_STUB_UPDATES || "";
/** @type {any[]} */
let updates = [];
if (script) updates = JSON.parse(readFileSync(script, "utf8"));

/**
 * @param {import("node:http").ServerResponse} response
 * @param {number} status
 * @param {any} value
 */
function reply(response, status, value) {
  response.writeHead(status, { "content-type": "application/json" });
  response.end(JSON.stringify(value));
}

const server = createServer((request, response) => {
  /** @type {Buffer[]} */
  const chunks = [];
  request.on("data", (chunk) => chunks.push(chunk));
  request.on("end", () => {
    let body;
    try { body = JSON.parse(Buffer.concat(chunks).toString("utf8") || "{}"); } catch {
      return reply(response, 400, { ok: false, error_code: 400 });
    }
    const prefix = `/bot${encodeURIComponent(token)}/`;
    if (request.method !== "POST" || !request.url?.startsWith(prefix)) {
      return reply(response, 404, { ok: false, error_code: 404 });
    }
    const method = request.url.slice(prefix.length);
    if (record) appendFileSync(record, `${JSON.stringify({ method, body })}\n`, { mode: 0o600 });
    if (method === "getMe") {
      return reply(response, 200, { ok: true, result: { id: 123456, is_bot: true, first_name: "Virtual Me Test", username: "virtualme_test_bot" } });
    }
    if (method === "getUpdates") {
      const offset = Number(body.offset || 0);
      const ready = updates.filter((item) => item.update_id >= offset);
      updates = updates.filter((item) => item.update_id < offset);
      return reply(response, 200, { ok: true, result: ready });
    }
    if (method === "sendMessage") {
      return reply(response, 200, { ok: true, result: { message_id: Date.now(), date: 0, chat: { id: Number(body.chat_id), type: "private" }, text: body.text } });
    }
    if (method === "sendChatAction") return reply(response, 200, { ok: true, result: true });
    return reply(response, 404, { ok: false, error_code: 404 });
  });
});

server.listen(Number(process.env.TELEGRAM_STUB_PORT || 0), process.env.TELEGRAM_STUB_HOST || "127.0.0.1", () => {
  const address = server.address();
  if (!address || typeof address === "string") throw new Error("Telegram stub has no TCP address");
  process.stdout.write(`TELEGRAM_STUB_READY=${address.port}\n`);
});

for (const signal of ["SIGINT", "SIGTERM"]) {
  process.on(signal, () => server.close(() => process.exit(0)));
}
