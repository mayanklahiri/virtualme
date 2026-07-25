import { bold, cyan, dim } from "../ansi.js";

const commands = [
  ["help", "Show this help"],
  ["version", "Print the CLI version"],
  ["doctor", "Check Node, Docker, and checkout setup"],
  ["start [--data <dir>] [--no-browser-sandbox] [--gpus <spec>] [--no-gpu] [--rebuild]", "Start the container; host NVIDIA is auto-passed through unless --no-gpu; --rebuild builds, stops, then starts"],
  ["stop", "Stop and remove the container"],
  ["status", "Show container and service health"],
  ["logs [-f]", "Show or follow container logs"],
  ["build", "Build the local development/start image"],
  ["keygen", "Generate a 256-bit base64url token"],
  ["update", "Pull the configured image"],
  ["soak [--no-build]", "Rebuild once, run full e2e, then live soak flows (source checkout)"],
  ["docs dev [--host <host>] [--port <port>]", "Serve the documentation site (source checkout)"],
  ["docs build", "Build the documentation site (source checkout)"],
];

/**
 * @param {string[]} _argv
 * @param {(message: string) => void} [print]
 */
export function run(_argv, print = console.log) {
  print(bold("Virtual Me — private personal background agent"));
  print("");
  print(`${bold("Usage:")} virtualme <command>`);
  print("");
  print(dim("Commands:"));
  for (const [name, description] of commands) {
    print(`  ${cyan(name.padEnd(14))} ${description}`);
  }
  return 0;
}
