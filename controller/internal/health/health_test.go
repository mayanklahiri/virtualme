package health

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCheckHTTP(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()
		if result := checkHTTP("test", server.URL); !result.OK {
			t.Fatalf("checkHTTP() = %+v", result)
		}
	})
	t.Run("bad status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()
		result := checkHTTP("test", server.URL)
		if result.OK || result.Detail != "status 500" {
			t.Fatalf("checkHTTP() = %+v", result)
		}
	})
	t.Run("closed port", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		target := "http://" + listener.Addr().String()
		_ = listener.Close()
		if result := checkHTTP("test", target); result.OK {
			t.Fatalf("checkHTTP() = %+v", result)
		}
	})
}

func TestCheckValkey(t *testing.T) {
	for _, test := range []struct {
		name     string
		response string
		ok       bool
	}{
		{name: "pong", response: "+PONG\r\n", ok: true},
		{name: "error", response: "-ERR nope\r\n", ok: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			listener := startFakeTCP(t, func(conn net.Conn) {
				_, _ = bufio.NewReader(conn).ReadString('\n')
				_, _ = conn.Write([]byte(test.response))
			})
			if result := checkValkey(listener.Addr().String()); result.OK != test.ok {
				t.Fatalf("checkValkey() = %+v", result)
			}
		})
	}
}

func TestCheckX11Socket(t *testing.T) {
	socketDir := t.TempDir()
	cfg := Config{Display: ":99", X11SocketDir: socketDir}
	if result := checkX11Socket(cfg); result.OK {
		t.Fatalf("missing socket check = %+v", result)
	}
	if err := os.WriteFile(filepath.Join(socketDir, "X99"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if result := checkX11Socket(cfg); !result.OK {
		t.Fatalf("existing socket check = %+v", result)
	}
}

func TestCheckChromium(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture requires POSIX")
	}
	for _, test := range []struct {
		name string
		exit int
		ok   bool
	}{
		{name: "visible", exit: 0, ok: true},
		{name: "missing", exit: 1, ok: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			script := filepath.Join(t.TempDir(), "xdotool")
			body := fmt.Sprintf("#!/bin/sh\nexit %d\n", test.exit)
			if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
				t.Fatal(err)
			}
			result := checkChromium(Config{Display: ":99", Xdotool: script})
			if result.OK != test.ok {
				t.Fatalf("checkChromium() = %+v", result)
			}
		})
	}
}

func TestGather(t *testing.T) {
	cfg, cleanup := allGreenConfig(t)
	defer cleanup()
	result := Gather(cfg)
	if !result.OK {
		t.Fatalf("Gather() = %+v", result)
	}
	want := []string{"xvfb", "x11vnc", "novnc", "valkey", "llama", "tts", "chromium", "mail"}
	if len(result.Services) != len(want) {
		t.Fatalf("got %d services", len(result.Services))
	}
	for index, name := range want {
		if result.Services[index].Name != name {
			t.Errorf("service[%d] = %q, want %q", index, result.Services[index].Name, name)
		}
	}

	cfg.NoVNCURL += "/missing"
	if red := Gather(cfg); red.OK {
		t.Fatalf("Gather(red) = %+v", red)
	}
}

func allGreenConfig(t *testing.T) (Config, func()) {
	t.Helper()
	socketDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(socketDir, "X99"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	vnc := startFakeTCP(t, func(net.Conn) {})
	valkey := startFakeTCP(t, func(conn net.Conn) {
		_, _ = bufio.NewReader(conn).ReadString('\n')
		_, _ = conn.Write([]byte("+PONG\r\n"))
	})
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "/missing") {
			http.NotFound(w, request)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	script := filepath.Join(t.TempDir(), "xdotool")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	sendmail := filepath.Join(t.TempDir(), "sendmail")
	if err := os.WriteFile(sendmail, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	spool := t.TempDir()
	cfg := Config{
		Display:        ":99",
		X11SocketDir:   socketDir,
		VNCAddr:        vnc.Addr().String(),
		NoVNCURL:       httpServer.URL,
		ValkeyAddr:     valkey.Addr().String(),
		LlamaHealthURL: httpServer.URL,
		TTSHealthURL:   httpServer.URL,
		Xdotool:        script,
		SendmailPath:   sendmail,
		MailSpoolDir:   spool,
	}
	return cfg, func() {
		_ = vnc.Close()
		_ = valkey.Close()
		httpServer.Close()
	}
}

func startFakeTCP(t *testing.T, handle func(net.Conn)) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				handle(conn)
			}()
		}
	}()
	return listener
}
