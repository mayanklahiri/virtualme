package ws

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const testKey = "dGhlIHNhbXBsZSBub25jZQ=="

func TestAcceptKey(t *testing.T) {
	const want = "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="
	if got := acceptKey(testKey); got != want {
		t.Fatalf("acceptKey() = %q, want %q", got, want)
	}
}

func TestHandshakeAndServerPush(t *testing.T) {
	hub := NewHub()
	server := httptest.NewServer(http.HandlerFunc(hub.HandleUpgrade))
	defer server.Close()
	conn, reader, response := dialWebsocket(t, server.URL)
	defer conn.Close()
	if response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if got := response.Header.Get("Sec-WebSocket-Accept"); got != acceptKey(testKey) {
		t.Fatalf("accept = %q", got)
	}
	waitForCount(t, hub, 1)
	hub.Broadcast([]byte("hi"))
	frame := make([]byte, 4)
	if _, err := io.ReadFull(reader, frame); err != nil {
		t.Fatal(err)
	}
	if got := string(frame); got != "\x81\x02hi" {
		t.Fatalf("frame = %q", got)
	}
}

func TestPingPong(t *testing.T) {
	hub := NewHub()
	server := httptest.NewServer(http.HandlerFunc(hub.HandleUpgrade))
	defer server.Close()
	conn, reader, _ := dialWebsocket(t, server.URL)
	defer conn.Close()
	waitForCount(t, hub, 1)

	mask := [4]byte{1, 2, 3, 4}
	payload := []byte{'a' ^ mask[0], 'b' ^ mask[1]}
	frame := append([]byte{0x89, 0x82}, mask[:]...)
	frame = append(frame, payload...)
	if _, err := conn.Write(frame); err != nil {
		t.Fatal(err)
	}
	pong := make([]byte, 4)
	if _, err := io.ReadFull(reader, pong); err != nil {
		t.Fatal(err)
	}
	if got := string(pong); got != "\x8a\x02ab" {
		t.Fatalf("pong = %q", got)
	}
}

func TestOnConnect(t *testing.T) {
	hub := NewHub()
	hub.SetOnConnect(func(c *Conn) {
		_ = c.WriteText([]byte("hello"))
	})
	server := httptest.NewServer(http.HandlerFunc(hub.HandleUpgrade))
	defer server.Close()
	conn, reader, _ := dialWebsocket(t, server.URL)
	defer conn.Close()

	frame := make([]byte, 7)
	if _, err := io.ReadFull(reader, frame); err != nil {
		t.Fatal(err)
	}
	if got := string(frame); got != "\x81\x05hello" {
		t.Fatalf("frame = %q", got)
	}
}

func TestStableIDsAndDisconnectHook(t *testing.T) {
	hub := NewHub()
	connected := make(chan string, 2)
	disconnected := make(chan string, 2)
	hub.SetOnConnect(func(c *Conn) { connected <- c.ID() })
	hub.SetOnDisconnect(func(id string) { disconnected <- id })
	server := httptest.NewServer(http.HandlerFunc(hub.HandleUpgrade))
	defer server.Close()
	first, _, _ := dialWebsocket(t, server.URL)
	second, _, _ := dialWebsocket(t, server.URL)
	firstID, secondID := <-connected, <-connected
	if firstID != "c1" || secondID != "c2" {
		t.Fatalf("ids = %q, %q", firstID, secondID)
	}
	_ = first.Close()
	select {
	case got := <-disconnected:
		if got != firstID {
			t.Fatalf("disconnect id = %q, want %q", got, firstID)
		}
	case <-time.After(time.Second):
		t.Fatal("disconnect hook not called")
	}
	_ = second.Close()
}

func TestClientMessageDispatch(t *testing.T) {
	hub := NewHub()
	type received struct {
		conn    *Conn
		payload string
	}
	got := make(chan received, 1)
	hub.SetHandler(func(c *Conn, payload []byte) {
		got <- received{conn: c, payload: string(payload)}
	})
	var registered *Conn
	hub.SetOnConnect(func(c *Conn) { registered = c })
	server := httptest.NewServer(http.HandlerFunc(hub.HandleUpgrade))
	defer server.Close()
	conn, _, _ := dialWebsocket(t, server.URL)
	defer conn.Close()
	waitForCount(t, hub, 1)

	message := `{"type":"chat","text":"x"}`
	mask := [4]byte{5, 6, 7, 8}
	frame := append([]byte{0x81, 0x80 | byte(len(message))}, mask[:]...)
	for index := range len(message) {
		frame = append(frame, message[index]^mask[index%4])
	}
	if _, err := conn.Write(frame); err != nil {
		t.Fatal(err)
	}

	select {
	case msg := <-got:
		if msg.payload != message {
			t.Fatalf("payload = %q, want %q", msg.payload, message)
		}
		if msg.conn != registered {
			t.Fatal("handler received a different *Conn than the registered one")
		}
	case <-time.After(time.Second):
		t.Fatal("handler never invoked")
	}
}

func TestBadHandshake(t *testing.T) {
	hub := NewHub()
	server := httptest.NewServer(http.HandlerFunc(hub.HandleUpgrade))
	defer server.Close()
	response, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.StatusCode)
	}
}

func dialWebsocket(t *testing.T, serverURL string) (net.Conn, *bufio.Reader, *http.Response) {
	t.Helper()
	parsed, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", parsed.Host)
	if err != nil {
		t.Fatal(err)
	}
	request := fmt.Sprintf("GET /ws HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: keep-alive, Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: %s\r\n\r\n", parsed.Host, testKey)
	if _, err := io.WriteString(conn, request); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	return conn, reader, response
}

func waitForCount(t *testing.T, hub *Hub, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for hub.Count() != want {
		if time.Now().After(deadline) {
			t.Fatalf("hub count = %d, want %d", hub.Count(), want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestHeaderHasToken(t *testing.T) {
	if !headerHasToken("keep-alive, Upgrade", "upgrade") {
		t.Fatal("token not found")
	}
	if headerHasToken(strings.Repeat("x", 4), "upgrade") {
		t.Fatal("unexpected token")
	}
}
