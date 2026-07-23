import { bold, cyan, dim } from "../ansi.js";

const commands = [
  ["help", "Show this help"],
  ["version", "Print the CLI version"],
  ["doctor", "Check Node, Docker, and checkout setup"],
  ["start", "Start the Virtual Me container"],
  ["stop", "Stop and remove the container"],
  ["status", "Show container and service health"],
  ["logs [-f]", "Show or follow container logs"],
  ["build", "Build the development image"],
  ["keygen", "Generate a 256-bit base64url token"],
  ["update", "Pull the configured image"],
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
