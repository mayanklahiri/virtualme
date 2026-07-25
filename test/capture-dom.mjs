// Development-only live DOM fixture capture. Not run by the offline gate.
// Usage: node test/capture-dom.mjs <fixture-name>
// Env: CAPTURE_URL=ws://127.0.0.1:8080/ws
import { randomUUID } from "node:crypto";
import { mkdir, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const name = process.argv[2];
if (!name || !/^[a-z0-9][a-z0-9-]*$/i.test(name)) {
  throw new Error("usage: node test/capture-dom.mjs <fixture-name>");
}
const wsURL = process.env.CAPTURE_URL ?? "ws://127.0.0.1:8080/ws";
const httpBase = new URL(wsURL);
httpBase.protocol = httpBase.protocol === "wss:" ? "https:" : "http:";
httpBase.pathname = "/";
httpBase.search = "";
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const output = path.join(root, "test", "fixtures", `${name}.dom.json`);

const relativePath = await new Promise((resolve, reject) => {
  const ws = new WebSocket(wsURL);
  const id = randomUUID();
  const timer = setTimeout(() => {
    ws.close();
    reject(new Error("dump_dom timed out after 60s"));
  }, 60000);
  ws.addEventListener("open", () => {
    ws.send(JSON.stringify({ type: "tool-invoke", id, tool: "dump_dom", args: {} }));
  });
  ws.addEventListener("message", (event) => {
    const frame = JSON.parse(String(event.data));
    if (frame.type !== "tool-result" || frame.id !== id) return;
    clearTimeout(timer);
    ws.close();
    if (!frame.ok) reject(new Error(frame.error || "dump_dom failed"));
    else resolve(String(frame.text));
  });
  ws.addEventListener("error", () => {
    clearTimeout(timer);
    reject(new Error(`cannot connect to ${wsURL}`));
  });
});

const endpoint = new URL("/api/data/file", httpBase);
endpoint.searchParams.set("path", relativePath);
endpoint.searchParams.set("download", "1");
const response = await fetch(endpoint);
if (!response.ok) throw new Error(`download ${relativePath}: HTTP ${response.status}`);
const fixture = await response.text();
JSON.parse(fixture);
await mkdir(path.dirname(output), { recursive: true });
await writeFile(output, fixture.endsWith("\n") ? fixture : fixture + "\n");
console.log(`wrote ${output}`);
