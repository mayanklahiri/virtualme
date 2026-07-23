// Package ws implements the small RFC 6455 subset required by the control UI.
package ws

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
)

const (
	websocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	maxPayload    = 64 * 1024
)

// Conn is one upgraded websocket connection.
type Conn struct {
	conn      net.Conn
	reader    *bufio.Reader
	writer    *bufio.Writer
	writeMu   sync.Mutex
	closeOnce sync.Once
	onText    func(payload []byte)
}

// Hub tracks connected browsers and broadcasts state snapshots.
type Hub struct {
	mu        sync.Mutex
	conns     map[*Conn]struct{}
	handler   func(c *Conn, payload []byte)
	onConnect func(c *Conn)
}

// NewHub returns an empty websocket hub.
func NewHub() *Hub {
	return &Hub{conns: make(map[*Conn]struct{})}
}

// SetHandler registers the receiver for client text frames.
func (h *Hub) SetHandler(fn func(c *Conn, payload []byte)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.handler = fn
}

// SetOnConnect registers a hook called after each connection registers.
func (h *Hub) SetOnConnect(fn func(c *Conn)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onConnect = fn
}

func acceptKey(key string) string {
	sum := sha1.Sum([]byte(key + websocketGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func headerHasToken(value, token string) bool {
	for part := range strings.SplitSeq(value, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

// Upgrade validates and upgrades an HTTP request to a websocket connection.
func Upgrade(w http.ResponseWriter, r *http.Request) (*Conn, error) {
	fail := func(message string) (*Conn, error) {
		http.Error(w, message, http.StatusBadRequest)
		return nil, errors.New(message)
	}
	if r.Method != http.MethodGet {
		return fail("websocket requires GET")
	}
	if !headerHasToken(r.Header.Get("Upgrade"), "websocket") {
		return fail("missing websocket upgrade")
	}
	if !headerHasToken(r.Header.Get("Connection"), "upgrade") {
		return fail("missing connection upgrade")
	}
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		return fail("unsupported websocket version")
	}
	key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	if key == "" {
		return fail("missing websocket key")
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return fail("websocket hijacking unavailable")
	}
	raw, readWriter, err := hijacker.Hijack()
	if err != nil {
		return nil, fmt.Errorf("hijack: %w", err)
	}
	if _, err := fmt.Fprintf(readWriter, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", acceptKey(key)); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("write handshake: %w", err)
	}
	if err := readWriter.Flush(); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("flush handshake: %w", err)
	}
	return &Conn{conn: raw, reader: readWriter.Reader, writer: readWriter.Writer}, nil
}

func (c *Conn) writeFrame(opcode byte, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if err := c.writer.WriteByte(0x80 | opcode); err != nil {
		return err
	}
	length := uint64(len(payload))
	switch {
	case length < 126:
		if err := c.writer.WriteByte(byte(length)); err != nil {
			return err
		}
	case length <= 65535:
		if err := c.writer.WriteByte(126); err != nil {
			return err
		}
		var encoded [2]byte
		binary.BigEndian.PutUint16(encoded[:], uint16(length))
		if _, err := c.writer.Write(encoded[:]); err != nil {
			return err
		}
	default:
		if err := c.writer.WriteByte(127); err != nil {
			return err
		}
		var encoded [8]byte
		binary.BigEndian.PutUint64(encoded[:], length)
		if _, err := c.writer.Write(encoded[:]); err != nil {
			return err
		}
	}
	if _, err := c.writer.Write(payload); err != nil {
		return err
	}
	return c.writer.Flush()
}

// WriteText writes an unmasked, unfragmented server text frame.
func (c *Conn) WriteText(payload []byte) error {
	return c.writeFrame(0x1, payload)
}

func (c *Conn) closeWithCode(code uint16) {
	c.closeOnce.Do(func() {
		var payload [2]byte
		binary.BigEndian.PutUint16(payload[:], code)
		_ = c.writeFrame(0x8, payload[:])
		_ = c.conn.Close()
	})
}

// Close sends a normal close frame and closes the transport. It is idempotent.
func (c *Conn) Close() error {
	c.closeWithCode(1000)
	return nil
}

func (c *Conn) readLength(short byte) (uint64, error) {
	switch short {
	case 126:
		var encoded [2]byte
		if _, err := io.ReadFull(c.reader, encoded[:]); err != nil {
			return 0, err
		}
		return uint64(binary.BigEndian.Uint16(encoded[:])), nil
	case 127:
		var encoded [8]byte
		if _, err := io.ReadFull(c.reader, encoded[:]); err != nil {
			return 0, err
		}
		return binary.BigEndian.Uint64(encoded[:]), nil
	default:
		return uint64(short), nil
	}
}

// ReadLoop services client frames until close or error, delivering text
// frames to the registered handler (if any) and discarding binary frames.
func (c *Conn) ReadLoop() {
	for {
		header := make([]byte, 2)
		if _, err := io.ReadFull(c.reader, header); err != nil {
			return
		}
		fin := header[0]&0x80 != 0
		rsv := header[0] & 0x70
		opcode := header[0] & 0x0f
		masked := header[1]&0x80 != 0
		if !fin || rsv != 0 || !masked {
			c.closeWithCode(1002)
			return
		}
		length, err := c.readLength(header[1] & 0x7f)
		if err != nil {
			return
		}
		if length > maxPayload {
			c.closeWithCode(1009)
			return
		}
		if opcode >= 0x8 && length > 125 {
			c.closeWithCode(1002)
			return
		}
		var mask [4]byte
		if _, err := io.ReadFull(c.reader, mask[:]); err != nil {
			return
		}
		payload := make([]byte, int(length))
		if _, err := io.ReadFull(c.reader, payload); err != nil {
			return
		}
		for index := range payload {
			payload[index] ^= mask[index%len(mask)]
		}

		switch opcode {
		case 0x8:
			c.closeWithCode(1000)
			return
		case 0x9:
			if err := c.writeFrame(0xA, payload); err != nil {
				return
			}
		case 0xA:
		case 0x1:
			if c.onText != nil {
				c.onText(payload)
			}
		case 0x2:
		default:
			c.closeWithCode(1002)
			return
		}
	}
}

// HandleUpgrade upgrades, registers, runs the on-connect hook, and services
// one websocket client.
func (h *Hub) HandleUpgrade(w http.ResponseWriter, r *http.Request) {
	conn, err := Upgrade(w, r)
	if err != nil {
		return
	}
	conn.onText = func(payload []byte) {
		h.mu.Lock()
		handler := h.handler
		h.mu.Unlock()
		if handler != nil {
			handler(conn, payload)
		}
	}
	h.mu.Lock()
	h.conns[conn] = struct{}{}
	onConnect := h.onConnect
	h.mu.Unlock()
	if onConnect != nil {
		onConnect(conn)
	}
	go func() {
		conn.ReadLoop()
		h.mu.Lock()
		delete(h.conns, conn)
		h.mu.Unlock()
		_ = conn.Close()
	}()
}

// Broadcast sends a text frame to every client, dropping failed connections.
func (h *Hub) Broadcast(payload []byte) {
	h.mu.Lock()
	conns := make([]*Conn, 0, len(h.conns))
	for conn := range h.conns {
		conns = append(conns, conn)
	}
	h.mu.Unlock()

	for _, conn := range conns {
		if err := conn.WriteText(payload); err != nil {
			h.mu.Lock()
			delete(h.conns, conn)
			h.mu.Unlock()
			_ = conn.Close()
		}
	}
}

// Count returns the current number of registered clients.
func (h *Hub) Count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.conns)
}
