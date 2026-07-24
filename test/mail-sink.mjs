// Minimal SMTP capture sink (Node built-ins only).
import { writeFileSync } from "node:fs";
import { createServer } from "node:net";

const output = process.argv[2];
const host = process.env.MAIL_SINK_HOST ?? "127.0.0.1";
const port = Number(process.env.MAIL_SINK_PORT ?? 2525);
if (!output) {
  console.error("usage: mail-sink.mjs <output-file>");
  process.exit(2);
}

const server = createServer((socket) => {
  socket.setEncoding("utf8");
  socket.write("220 virtualme test sink\r\n");
  let buffer = "";
  let data = false;
  let message = "";
  socket.on("data", (chunk) => {
    buffer += chunk;
    for (;;) {
      const end = buffer.indexOf("\r\n");
      if (end < 0) break;
      const line = buffer.slice(0, end);
      buffer = buffer.slice(end + 2);
      if (data) {
        if (line === ".") {
          data = false;
          const captured = message;
          const accept = () => {
            writeFileSync(output, captured);
            socket.write("250 queued\r\n");
          };
          const delay = Number(process.env.MAIL_SINK_ACCEPT_DELAY_MS ?? 0);
          if (delay > 0) setTimeout(accept, delay);
          else accept();
        } else {
          message += `${line.startsWith("..") ? line.slice(1) : line}\r\n`;
        }
      } else if (/^(EHLO|HELO)\b/i.test(line)) {
        socket.write("250-virtualme\r\n250 8BITMIME\r\n");
      } else if (/^DATA$/i.test(line)) {
        data = true;
        message = "";
        socket.write("354 end with <CRLF>.<CRLF>\r\n");
      } else if (/^STARTTLS$/i.test(line)) {
        socket.write("454 TLS unavailable in test sink\r\n");
      } else if (/^QUIT$/i.test(line)) {
        socket.end("221 bye\r\n");
      } else {
        socket.write("250 ok\r\n");
      }
    }
  });
});

server.listen(port, host, () => console.log(`mail-sink: READY ${host}:${port}`));
