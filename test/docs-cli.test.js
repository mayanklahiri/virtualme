// @ts-nocheck
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const root = new URL("../", import.meta.url);

test("docs command is registered with exact help rows", async () => {
  const [main, help] = await Promise.all([
    readFile(new URL("src/main.js", root), "utf8"),
    readFile(new URL("src/commands/help.js", root), "utf8"),
  ]);
  assert.match(main, /import \{ run as docs \} from "\.\/commands\/docs\.js"/);
  assert.match(main, /\bdocs\b.*\}/);
  assert.match(help, /docs dev \[--host <host>\] \[--port <port>\].*Serve the documentation site \(source checkout\)/);
  assert.match(help, /docs build.*Build the documentation site \(source checkout\)/);
});

test("docs parser implements the complete argument contract", async () => {
  const { parseArgs, usage } = await import("../src/commands/docs.js");
  assert.equal(usage, "Usage:\n  virtualme docs dev [--host <host>] [--port <port>]\n  virtualme docs build");
  assert.deepEqual(parseArgs(["dev"]), { command: "dev", host: "127.0.0.1", port: "4321" });
  assert.deepEqual(parseArgs(["dev", "--port", "9000", "--host", "0.0.0.0"]), { command: "dev", host: "0.0.0.0", port: "9000" });
  for (const argv of [
    [], ["wat"], ["dev", "--host"], ["dev", "--host", ""], ["dev", "--host", "x", "--host", "y"],
    ["dev", "--port"], ["dev", "--port", "0"], ["dev", "--port", "65536"], ["dev", "--port", "1.5"],
    ["dev", "--port=3"], ["dev", "--host=x"], ["dev", "--open"], ["dev", "extra"], ["build", "extra"],
  ]) assert.throws(() => parseArgs(argv), { name: "UsageError" });
  assert.deepEqual(parseArgs(["--help"]), { help: true });
  assert.deepEqual(parseArgs(["dev", "--help"]), { help: true });
  assert.deepEqual(parseArgs(["build", "--help"]), { help: true });
});

test("docs runner validates checkout, dependencies, spawn and status", async () => {
  const { runWith } = await import("../src/commands/docs.js");
  const calls = [];
  const base = {
    exists: (path) => !path.endsWith("astro"),
    spawn: (...args) => { calls.push(args); return { status: 0, signal: null, error: undefined }; },
    print: () => {},
    error: (message) => calls.push(["error", message]),
  };
  assert.equal(await runWith(["build"], { ...base, exists: () => false }), 1);
  assert.match(calls.at(-1)[1], /source checkout/);
  assert.equal(await runWith(["build"], base), 1);
  assert.match(calls.at(-1)[1], /npm ci --prefix docs/);
  assert.equal(await runWith(["dev"], { ...base, exists: () => true }), 0);
  const [file, args, options] = calls.at(-1);
  assert.equal(file, "npm");
  assert.deepEqual(args, ["run", "dev", "--", "--host", "127.0.0.1", "--port", "4321"]);
  assert.equal(options.stdio, "inherit");
  assert.match(options.cwd, /\/docs$/);
  assert.equal(await runWith(["build"], { ...base, exists: () => true, spawn: () => ({ status: 7 }) }), 1);
  assert.equal(await runWith(["build"], { ...base, exists: () => true, spawn: () => ({ signal: "SIGTERM" }) }), 143);
  assert.equal(await runWith(["build"], { ...base, exists: () => true, spawn: () => ({ error: new Error("boom") }) }), 1);
});
