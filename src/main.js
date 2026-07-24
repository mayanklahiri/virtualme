import { red } from "./ansi.js";
import { run as help } from "./commands/help.js";
import { run as version } from "./commands/version.js";
import { run as doctor } from "./commands/doctor.js";
import { run as keygen } from "./commands/keygen.js";
import { run as start } from "./commands/start.js";
import { run as stop } from "./commands/stop.js";
import { run as status } from "./commands/status.js";
import { run as logs } from "./commands/logs.js";
import { run as build } from "./commands/build.js";
import { run as update } from "./commands/update.js";
import { run as soak } from "./commands/soak.js";

/** @type {Record<string, (argv: string[]) => number | Promise<number>>} */
const commands = { help, version, doctor, keygen, start, stop, status, logs, build, update, soak };

/** @param {string[]} argv */
export async function main(argv) {
  const [command, ...rest] = argv;
  if (command === undefined || ["help", "-h", "--help"].includes(command)) {
    return help([], console.log);
  }
  if (["version", "-v", "--version"].includes(command)) {
    return version(rest);
  }
  const handler = commands[command];
  if (handler) return handler(rest);
  console.error(red(`error: unknown command '${command}'`));
  help([], console.error);
  return 2;
}
