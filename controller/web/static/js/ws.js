export function connect(onMessage, onStatus) {
  let delay = 1000;
  let retryTimer;
  let socket = null;

  const schedule = () => {
    if (retryTimer !== undefined) {
      return;
    }
    onStatus("reconnecting");
    retryTimer = window.setTimeout(() => {
      retryTimer = undefined;
      open();
    }, delay);
    delay = Math.min(delay * 2, 30000);
  };

  const open = () => {
    onStatus(delay === 1000 ? "connecting" : "reconnecting");
    const scheme = location.protocol === "https:" ? "wss://" : "ws://";
    socket = new WebSocket(scheme + location.host + "/ws");
    socket.addEventListener("open", () => {
      delay = 1000;
      onStatus("live");
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
    send(value) {
      if (socket === null || socket.readyState !== WebSocket.OPEN) {
        return false;
      }
      socket.send(JSON.stringify(value));
      return true;
    },
  };
}
