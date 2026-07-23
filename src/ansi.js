export const enabled =
  process.stdout.isTTY === true &&
  !("NO_COLOR" in process.env) &&
  process.env.TERM !== "dumb";

/**
 * @param {number} code
 * @returns {(value: unknown) => string}
 */
export function wrap(code) {
  return (value) => enabled ? `\x1b[${code}m${String(value)}\x1b[0m` : String(value);
}

export const bold = wrap(1);
export const dim = wrap(2);
export const red = wrap(31);
export const green = wrap(32);
export const yellow = wrap(33);
export const cyan = wrap(36);
