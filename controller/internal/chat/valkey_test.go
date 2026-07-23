package chat

import (
	"io"
	"net"
	"strings"
	"sync"
	"testing"
)

// fakeValkey accepts one connection, captures exactly len(wantRequest) bytes,
// writes reply, and closes. wait() returns the captured request bytes.
func fakeValkey(t *testing.T, wantRequestLen int, reply string) (addr string, wait func() string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	var once sync.Once
	done := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		request := make([]byte, wantRequestLen)
		if _, err := io.ReadFull(conn, request); err != nil {
			done <- "read error: " + err.Error()
			return
		}
		_, _ = io.WriteString(conn, reply)
		done <- string(request)
	}()
	return listener.Addr().String(), func() string {
		var result string
		once.Do(func() { result = <-done })
		return result
	}
}

func TestRPushWireFormat(t *testing.T) {
	want := "*3\r\n$5\r\nRPUSH\r\n$1\r\nk\r\n$2\r\nvv\r\n"
	addr, wait := fakeValkey(t, len(want), ":1\r\n")
	client := newValkeyClient(addr)
	if err := client.rpush("k", "vv"); err != nil {
		t.Fatal(err)
	}
	if got := wait(); got != want {
		t.Fatalf("request = %q, want %q", got, want)
	}
}

func TestLTrimWireFormat(t *testing.T) {
	want := "*4\r\n$5\r\nLTRIM\r\n$1\r\nk\r\n$4\r\n-200\r\n$2\r\n-1\r\n"
	addr, wait := fakeValkey(t, len(want), "+OK\r\n")
	client := newValkeyClient(addr)
	if err := client.ltrim("k", -200, -1); err != nil {
		t.Fatal(err)
	}
	if got := wait(); got != want {
		t.Fatalf("request = %q, want %q", got, want)
	}
}

func TestLRangeWireFormatAndParse(t *testing.T) {
	want := "*4\r\n$6\r\nLRANGE\r\n$1\r\nk\r\n$1\r\n0\r\n$2\r\n-1\r\n"
	addr, wait := fakeValkey(t, len(want), "*2\r\n$1\r\na\r\n$2\r\nbb\r\n")
	client := newValkeyClient(addr)
	got, err := client.lrange("k", 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "bb" {
		t.Fatalf("lrange = %v", got)
	}
	if request := wait(); request != want {
		t.Fatalf("request = %q, want %q", request, want)
	}
}

func TestErrorReplySurfaces(t *testing.T) {
	want := "*3\r\n$5\r\nRPUSH\r\n$1\r\nk\r\n$1\r\nv\r\n"
	addr, _ := fakeValkey(t, len(want), "-ERR boom\r\n")
	client := newValkeyClient(addr)
	err := client.rpush("k", "v")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want ERR boom", err)
	}
}

func TestConnectionRefused(t *testing.T) {
	client := newValkeyClient("127.0.0.1:1")
	if err := client.rpush("k", "v"); err == nil {
		t.Fatal("expected dial error")
	}
}
