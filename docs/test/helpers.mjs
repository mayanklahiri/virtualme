import { createReadStream } from "node:fs";
import { stat } from "node:fs/promises";
import { createServer } from "node:http";
import { extname, join, normalize } from "node:path";

const root = new URL("../dist/", import.meta.url).pathname;
const types = { ".css": "text/css", ".html": "text/html", ".js": "text/javascript", ".jpg": "image/jpeg", ".svg": "image/svg+xml", ".woff2": "font/woff2" };

createServer(async (request, response) => {
  const pathname = decodeURIComponent(new URL(request.url, "http://localhost").pathname);
  if (!pathname.startsWith("/virtualme/")) {
    response.writeHead(404).end();
    return;
  }
  let relative = normalize(pathname.slice("/virtualme/".length)).replace(/^(\.\.(\/|\\|$))+/, "");
  if (relative === "." || !relative) relative = "index.html";
  else if (relative.endsWith("/")) relative += "index.html";
  const file = join(root, relative);
  try {
    const info = await stat(file);
    if (!info.isFile()) throw new Error("not a file");
    response.writeHead(200, { "content-type": types[extname(file)] ?? "application/octet-stream" });
    createReadStream(file).pipe(response);
  } catch {
    response.writeHead(404).end();
  }
}).listen(41730, "127.0.0.1", () => process.stdout.write("ready\n"));
