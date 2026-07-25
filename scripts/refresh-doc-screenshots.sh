#!/usr/bin/env bash
# Capture console route screenshots from a running Virtual Me on :8080 and
# write multi-resolution JPEGs under docs/src/screenshots/.
#
# Assumes http://127.0.0.1:8080/ is healthy. Re-runnable.
#
# Usage: bash scripts/refresh-doc-screenshots.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

OUT="$ROOT/docs/src/screenshots"
BASE_URL="${VM_DOC_SCREENSHOT_URL:-http://127.0.0.1:8080}"
VIEW_W=1280
VIEW_H=720
# Canonical README width plus larger variants.
WIDTHS=(480 960 1280)
JPEG_QUALITY=95

need() { command -v "$1" >/dev/null 2>&1 || { echo "refresh-doc-screenshots: missing $1" >&2; exit 1; }; }
need python3
need gm
need google-chrome

mkdir -p "$OUT"

echo "refresh-doc-screenshots: probing ${BASE_URL}/healthz"
code="$(curl -sS -o /tmp/vm-doc-healthz.json -w "%{http_code}" --max-time 5 "${BASE_URL}/healthz" || true)"
[[ "$code" == "200" ]] || {
  echo "refresh-doc-screenshots: ${BASE_URL}/healthz returned HTTP ${code:-none}; start Virtual Me on :8080 first" >&2
  exit 1
}

# slug|path  (home-route, chat, desktop feed the README screenshot strip at 480px)
ROUTES=(
  "home-route|/"
  "projects|/projects"
  "jobs|/jobs"
  "tools|/tools"
  "data|/data"
  "config|/config"
  "status|/status"
  "chat|/chat"
  "speech|/speech"
  "mail|/mail"
  "desktop|/desktop-view"
)

python3 - "$OUT" "$BASE_URL" "$VIEW_W" "$VIEW_H" "${ROUTES[@]}" <<'PY'
import base64, json, os, socket, struct, subprocess, sys, time, urllib.request
from pathlib import Path

out_dir = Path(sys.argv[1])
base_url = sys.argv[2].rstrip("/")
view_w, view_h = int(sys.argv[3]), int(sys.argv[4])
routes = []
for item in sys.argv[5:]:
    slug, path = item.split("|", 1)
    routes.append((slug, path))

def recv_exact(sock, n):
    buf = bytearray()
    while len(buf) < n:
        chunk = sock.recv(n - len(buf))
        if not chunk:
            raise EOFError("socket closed")
        buf.extend(chunk)
    return bytes(buf)

def ws_connect(url):
    assert url.startswith("ws://")
    hostport, path = url[5:].split("/", 1)
    host, port = hostport.split(":")
    port = int(port)
    sock = socket.create_connection((host, port), timeout=10)
    key = base64.b64encode(os.urandom(16)).decode()
    req = (
        f"GET /{path} HTTP/1.1\r\n"
        f"Host: {host}:{port}\r\n"
        f"Upgrade: websocket\r\n"
        f"Connection: Upgrade\r\n"
        f"Sec-WebSocket-Key: {key}\r\n"
        f"Sec-WebSocket-Version: 13\r\n\r\n"
    ).encode()
    sock.sendall(req)
    data = b""
    while b"\r\n\r\n" not in data:
        data += sock.recv(4096)
    return sock

def ws_send(sock, payload: bytes):
    mask = os.urandom(4)
    masked = bytes(b ^ mask[i % 4] for i, b in enumerate(payload))
    header = bytearray([0x81])
    n = len(masked)
    if n < 126:
        header.append(0x80 | n)
    elif n < 65536:
        header.append(0x80 | 126)
        header.extend(struct.pack("!H", n))
    else:
        header.append(0x80 | 127)
        header.extend(struct.pack("!Q", n))
    header.extend(mask)
    sock.sendall(header + masked)

def ws_recv(sock):
    msg = bytearray()
    while True:
        b1, b2 = recv_exact(sock, 2)
        fin = b1 & 0x80
        opcode = b1 & 0x0F
        masked = b2 & 0x80
        length = b2 & 0x7F
        if length == 126:
            length = struct.unpack("!H", recv_exact(sock, 2))[0]
        elif length == 127:
            length = struct.unpack("!Q", recv_exact(sock, 8))[0]
        mask_key = recv_exact(sock, 4) if masked else None
        payload = recv_exact(sock, length)
        if mask_key:
            payload = bytes(b ^ mask_key[i % 4] for i, b in enumerate(payload))
        if opcode == 0x8:
            raise EOFError("ws close")
        if opcode in (0x1, 0x2, 0x0):
            msg.extend(payload)
        if fin:
            return bytes(msg)

PORT = 9233
log = open("/tmp/refresh-doc-screenshots-chrome.log", "w")
proc = subprocess.Popen(
    [
        "google-chrome",
        "--headless=new",
        "--disable-gpu",
        "--hide-scrollbars",
        f"--remote-debugging-port={PORT}",
        f"--window-size={view_w},{view_h}",
        "about:blank",
    ],
    stdout=log,
    stderr=log,
)
try:
    for _ in range(50):
        try:
            with urllib.request.urlopen(f"http://127.0.0.1:{PORT}/json/version", timeout=0.2) as r:
                json.load(r)
            break
        except Exception:
            time.sleep(0.1)
    else:
        raise SystemExit("refresh-doc-screenshots: chrome failed to start")

    req = urllib.request.Request(f"http://127.0.0.1:{PORT}/json/new?{base_url}/", method="PUT")
    with urllib.request.urlopen(req) as r:
        tab = json.load(r)
    sock = ws_connect(tab["webSocketDebuggerUrl"])
    state = {"n": 0}

    def cdp(method, params=None, timeout=30):
        state["n"] += 1
        msg_id = state["n"]
        ws_send(sock, json.dumps({"id": msg_id, "method": method, "params": params or {}}).encode())
        deadline = time.time() + timeout
        while time.time() < deadline:
            msg = json.loads(ws_recv(sock))
            if msg.get("id") == msg_id:
                if "error" in msg:
                    raise RuntimeError(msg["error"])
                return msg.get("result", {})
        raise TimeoutError(method)

    cdp("Page.enable")
    cdp("Runtime.enable")
    cdp(
        "Emulation.setDeviceMetricsOverride",
        {"width": view_w, "height": view_h, "deviceScaleFactor": 1, "mobile": False},
    )

    for slug, path in routes:
        url = base_url + path
        print(f"refresh-doc-screenshots: capture {slug} <- {url}", flush=True)
        cdp("Page.navigate", {"url": url})
        # Wait for SPA route + live home health when on /
        ready_expr = (
            '(() => { const page=document.querySelector("[data-page]:not([hidden])")||document.querySelector("[data-page]");'
            ' const h=document.querySelector("#home-health");'
            ' const host=document.querySelector("#home-host");'
            ' const onHome=!!document.querySelector("[data-page=home]:not([hidden])");'
            ' if(onHome) return !!(h&&h.classList.contains("ok")&&host&&host.textContent&&host.textContent!=="…");'
            ' return !!page; })()'
        )
        ready = False
        for _ in range(100):
            r = cdp("Runtime.evaluate", {"expression": ready_expr, "returnByValue": True})
            if (r.get("result") or {}).get("value"):
                ready = True
                break
            time.sleep(0.2)
        if not ready:
            print(f"refresh-doc-screenshots: warning: {slug} may not be fully live", flush=True)
        # Settle delay: lets websocket-fed panes (and the noVNC desktop) connect.
        time.sleep(2)
        shot = cdp("Page.captureScreenshot", {"format": "png", "fromSurface": True}, timeout=60)
        png = base64.b64decode(shot["data"])
        raw = out_dir / f".{slug}-raw.png"
        raw.write_bytes(png)
        print(f"refresh-doc-screenshots: wrote {raw.name} ({len(png)} bytes)", flush=True)
finally:
    proc.terminate()
    try:
        proc.wait(timeout=3)
    except Exception:
        proc.kill()
PY

echo "refresh-doc-screenshots: resizing to ${WIDTHS[*]} px wide (JPEG q${JPEG_QUALITY})"
for item in "${ROUTES[@]}"; do
  slug="${item%%|*}"
  raw="$OUT/.${slug}-raw.png"
  [[ -s "$raw" ]] || { echo "refresh-doc-screenshots: missing $raw" >&2; exit 1; }
  for w in "${WIDTHS[@]}"; do
    if [[ "$w" -eq 480 ]]; then
      dest="$OUT/${slug}.jpg"
    else
      dest="$OUT/${slug}-${w}.jpg"
    fi
    gm convert "$raw" -filter Lanczos -resize "${w}x" -quality "$JPEG_QUALITY" "$dest"
    echo "refresh-doc-screenshots: $(basename "$dest") $(gm identify -format '%wx%h' "$dest")"
  done
  rm -f "$raw"
done

echo "refresh-doc-screenshots: OK"
