import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const docs = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const root = resolve(docs, "..");
const args = process.argv.slice(2);
if (args.some((arg) => arg !== "--check") || args.length > 1) {
  console.error("Usage: node scripts/ensure-generated.mjs [--check]");
  process.exit(2);
}
const child = spawnSync(process.execPath, [resolve(root, "scripts/generate-themes.mjs"), ...(args[0] ? ["--check"] : [])], { stdio: "inherit" });
if (child.status !== 0) process.exit(child.status ?? 1);

const path = resolve(docs, "src/generated/config-reference.json");
const reference = JSON.parse(await readFile(path, "utf8"));
const placeholder = {
  _generated: "placeholder for spec 031; DO NOT EDIT",
  schemaVersion: 1,
  status: "pending-spec-031",
  sections: [],
};
if (reference.status === "pending-spec-031") {
  if (JSON.stringify(reference) !== JSON.stringify(placeholder)) throw new Error("config-reference placeholder does not match spec 030");
} else {
  if (reference.schemaVersion !== 1 || !/^[0-9a-f]{64}$/.test(reference.schemaSha256) || !Array.isArray(reference.sections)) throw new Error("malformed complete config-reference export");
  const anchors = new Set();
  for (const section of reference.sections) {
    if (!section.anchor || anchors.has(section.anchor) || !Array.isArray(section.settings) || typeof section.exemplarYaml !== "string") throw new Error("malformed or duplicate config-reference section anchor");
    anchors.add(section.anchor);
    for (const setting of section.settings) {
      if (!setting.anchor || anchors.has(setting.anchor)) throw new Error("malformed or duplicate config-reference setting anchor");
      anchors.add(setting.anchor);
    }
  }
  const schemaPath = resolve(root, "controller/internal/config/schema.json");
  const digest = createHash("sha256").update(await readFile(schemaPath)).digest("hex");
  if (digest !== reference.schemaSha256) throw new Error("config-reference schemaSha256 is stale");
}
