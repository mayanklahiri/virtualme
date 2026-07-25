package valkey

import (
	"io"
	"net"
	"strings"
	"sync"
	"testing"
)

func fakeValkey(t *testing.T, want string, reply string) (string, func() string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	done := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		request := make([]byte, len(want))
		if _, err := io.ReadFull(conn, request); err != nil {
			done <- err.Error()
			return
		}
		_, _ = io.WriteString(conn, reply)
		done <- string(request)
	}()
	var once sync.Once
	var got string
	return listener.Addr().String(), func() string {
		once.Do(func() { got = <-done })
		return got
	}
}

func wire(args ...string) string {
	var result strings.Builder
	result.WriteString("*" + intText(len(args)) + "\r\n")
	for _, arg := range args {
		result.WriteString("$" + intText(len(arg)) + "\r\n" + arg + "\r\n")
	}
	return result.String()
}

func intText(value int) string {
	const digits = "0123456789"
	if value == 0 {
		return "0"
	}
	var result string
	for value > 0 {
		result = string(digits[value%10]) + result
		value /= 10
	}
	return result
}

func TestCommandWireFormats(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		reply string
		call  func(*Client) error
	}{
		{"rpush", []string{"RPUSH", "k", "v"}, ":1\r\n", func(c *Client) error { _, err := c.RPush("k", "v"); return err }},
		{"lpush", []string{"LPUSH", "k", "v"}, ":1\r\n", func(c *Client) error { _, err := c.LPush("k", "v"); return err }},
		{"ltrim", []string{"LTRIM", "k", "-2", "-1"}, "+OK\r\n", func(c *Client) error { return c.LTrim("k", -2, -1) }},
		{"llen", []string{"LLEN", "k"}, ":2\r\n", func(c *Client) error { _, err := c.LLen("k"); return err }},
		{"lrem", []string{"LREM", "k", "0", "v"}, ":1\r\n", func(c *Client) error { _, err := c.LRem("k", 0, "v"); return err }},
		{"lmove", []string{"LMOVE", "a", "b", "LEFT", "RIGHT"}, "$1\r\nv\r\n", func(c *Client) error { _, err := c.LMove("a", "b", "LEFT", "RIGHT"); return err }},
		{"del", []string{"DEL", "a", "b"}, ":2\r\n", func(c *Client) error { _, err := c.Del("a", "b"); return err }},
		{"hincrby", []string{"HINCRBY", "h", "f", "2"}, ":2\r\n", func(c *Client) error { _, err := c.HIncrBy("h", "f", 2); return err }},
		{"hset", []string{"HSET", "h", "f", "v"}, ":1\r\n", func(c *Client) error { _, err := c.HSet("h", "f", "v"); return err }},
		{"hget", []string{"HGET", "h", "f"}, "$1\r\nv\r\n", func(c *Client) error { _, err := c.HGet("h", "f"); return err }},
		{"hdel", []string{"HDEL", "h", "f"}, ":1\r\n", func(c *Client) error { _, err := c.HDel("h", "f"); return err }},
		{"set", []string{"SET", "k", "v"}, "+OK\r\n", func(c *Client) error { return c.Set("k", "v") }},
		{"setex", []string{"SET", "k", "v", "EX", "300"}, "+OK\r\n", func(c *Client) error { return c.SetEx("k", "v", 300) }},
		{"get", []string{"GET", "k"}, "$1\r\nv\r\n", func(c *Client) error { _, err := c.Get("k"); return err }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			want := wire(test.args...)
			addr, wait := fakeValkey(t, want, test.reply)
			if err := test.call(New(addr)); err != nil {
				t.Fatal(err)
			}
			if got := wait(); got != want {
				t.Fatalf("request = %q, want %q", got, want)
			}
		})
	}
}

func TestArrayAndNilReplies(t *testing.T) {
	t.Run("lrange", func(t *testing.T) {
		want := wire("LRANGE", "k", "0", "-1")
		addr, _ := fakeValkey(t, want, "*2\r\n$1\r\na\r\n$2\r\nbb\r\n")
		got, err := New(addr).LRange("k", 0, -1)
		if err != nil || len(got) != 2 || got[1] != "bb" {
			t.Fatalf("LRange = %v, %v", got, err)
		}
	})
	t.Run("hgetall", func(t *testing.T) {
		want := wire("HGETALL", "h")
		addr, _ := fakeValkey(t, want, "*2\r\n$1\r\nf\r\n$1\r\n7\r\n")
		got, err := New(addr).HGetAll("h")
		if err != nil || got["f"] != "7" {
			t.Fatalf("HGetAll = %v, %v", got, err)
		}
	})
	t.Run("nil", func(t *testing.T) {
		want := wire("LMOVE", "a", "b", "LEFT", "LEFT")
		addr, _ := fakeValkey(t, want, "$-1\r\n")
		got, err := New(addr).LMove("a", "b", "LEFT", "LEFT")
		if err != nil || got != nil {
			t.Fatalf("LMove = %v, %v", got, err)
		}
	})
}

func TestErrors(t *testing.T) {
	want := wire("RPUSH", "k", "v")
	addr, _ := fakeValkey(t, want, "-ERR boom\r\n")
	if _, err := New(addr).RPush("k", "v"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
	if _, err := New("127.0.0.1:1").RPush("k", "v"); err == nil {
		t.Fatal("expected dial error")
	}
	if _, err := New("unused").HSet("k", "odd"); err == nil {
		t.Fatal("expected odd HSET error")
	}
}
