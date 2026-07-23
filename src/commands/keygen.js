import crypto from "node:crypto";

/** @param {string[]} _argv */
export function run(_argv) {
  console.log(crypto.randomBytes(32).toString("base64url"));
  return 0;
}
