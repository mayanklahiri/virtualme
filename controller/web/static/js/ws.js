export function connect(onMessage, onStatus) {
  let delay = 1000;
  let retryTimer;
  let socket = null;
  let connectedSince = 0;

  const schedule = () => {
    if (retryTimer !== undefined) {
      return;
    }
    connectedSince = 0;
    onStatus("reconnecting", connectedSince);
    retryTimer = window.setTimeout(() => {
      retryTimer = undefined;
      open();
    }, delay);
    delay = Math.min(delay * 2, 30000);
  };

  const open = () => {
    connectedSince = 0;
    onStatus(delay === 1000 ? "connecting" : "reconnecting", connectedSince);
    const scheme = location.protocol === "https:" ? "wss://" : "ws://";
    socket = new WebSocket(scheme + location.host + "/ws");
    socket.addEventListener("open", () => {
      delay = 1000;
      connectedSince = Date.now();
      onStatus("live", connectedSince);
    });
    socket.addEventListener("message", (event) => {
      try {
        onMessage(JSON.parse(event.data));
      } catch {
        // Ignore malformed server messages; the next snapshot is authoritative.
      }
    });
    socket.addEventListener("close", schedule);
    socket.addEventListener("error", () => {
      socket.close();
      schedule();
    });
  };

  open();

  return {
    get connectedSince() {
      return connectedSince;
    },
    send(value) {
      if (socket === null || socket.readyState !== WebSocket.OPEN) {
        return false;
      }
      socket.send(JSON.stringify(value));
      return true;
    },
  };
}
