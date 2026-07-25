// Pure GFM pipe-table parsing, DOM-free so Node tests can import it.

/** @param {string} line */
export function hasUnescapedPipe(line) {
  for (let index = 0; index < line.length; index++) {
    if (line[index] === "\\") {
      index++;
    } else if (line[index] === "|") {
      return true;
    }
  }
  return false;
}

/**
 * @param {string} line
 * @returns {string[]}
 */
function splitRow(line) {
  /** @type {string[]} */
  const cells = [];
  let current = "";
  for (let index = 0; index < line.length; index++) {
    const character = line[index];
    if (character === "\\" && line[index + 1] === "|") {
      current += "|";
      index++;
    } else if (character === "|") {
      cells.push(current.trim());
      current = "";
    } else {
      current += character;
    }
  }
  cells.push(current.trim());
  if (cells.length > 1 && cells[0] === "" && line.trimStart().startsWith("|")) {
    cells.shift();
  }
  if (cells.length > 1 && cells[cells.length - 1] === "" && line.trimEnd().endsWith("|")) {
    cells.pop();
  }
  return cells;
}

const separatorCell = /^:?-{3,}:?$/;

/**
 * Reads a GFM pipe table from the start of `lines`.
 * @param {string[]} lines
 * @returns {{header: string[], align: (null|"left"|"center"|"right")[], rows: string[][], consumed: number} | null}
 */
export function parseTable(lines) {
  if (lines.length < 2 || !hasUnescapedPipe(lines[0]) || !hasUnescapedPipe(lines[1])) {
    return null;
  }
  const header = splitRow(lines[0]);
  const separator = splitRow(lines[1]);
  if (header.length === 0 || separator.length !== header.length ||
    !separator.every((cell) => separatorCell.test(cell))) {
    return null;
  }
  const align = separator.map((cell) => {
    const left = cell.startsWith(":");
    const right = cell.endsWith(":");
    if (left && right) return /** @type {const} */ ("center");
    if (right) return /** @type {const} */ ("right");
    if (left) return /** @type {const} */ ("left");
    return null;
  });
  /** @type {string[][]} */
  const rows = [];
  let consumed = 2;
  while (consumed < lines.length && lines[consumed].trim() !== "" && hasUnescapedPipe(lines[consumed])) {
    const cells = splitRow(lines[consumed]);
    while (cells.length < header.length) {
      cells.push("");
    }
    rows.push(cells.slice(0, header.length));
    consumed++;
  }
  return { header, align, rows, consumed };
}
