// Package health probes the services supervised inside the Virtual Me container.
package health

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Config identifies every service endpoint used by Gather.
type Config struct {
	Display        string
	X11SocketDir   string
	VNCAddr        string
	NoVNCURL       string
	ValkeyAddr     string
	LlamaHealthURL string
	TTSHealthURL   string
	Xdotool        string
	SendmailPath   string
	MailSpoolDir   string
}

// Service is the probe result for one supervised internal service.
type Service struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// Health is the aggregate health report served at /healthz.
type Health struct {
	OK       bool      `json:"ok"`
	Services []Service `json:"services"`
}

const probeTimeout = 2 * time.Second

func checkX11Socket(cfg Config) Service {
	display := strings.TrimPrefix(cfg.Display, ":")
	display = strings.SplitN(display, ".", 2)[0]
	path := filepath.Join(cfg.X11SocketDir, "X"+display)
	if _, err := os.Stat(path); err != nil {
		return Service{Name: "xvfb", Detail: err.Error()}
	}
	return Service{Name: "xvfb", OK: true}
}

func checkTCP(name, addr string) Service {
	conn, err := net.DialTimeout("tcp", addr, probeTimeout)
	if err != nil {
		return Service{Name: name, Detail: err.Error()}
	}
	_ = conn.Close()
	return Service{Name: name, OK: true}
}

func checkHTTP(name, target string) Service {
	client := &http.Client{Timeout: probeTimeout}
	response, err := client.Get(target)
	if err != nil {
		return Service{Name: name, Detail: err.Error()}
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return Service{Name: name, Detail: fmt.Sprintf("status %d", response.StatusCode)}
	}
	return Service{Name: name, OK: true}
}

func checkValkey(addr string) Service {
	conn, err := net.DialTimeout("tcp", addr, probeTimeout)
	if err != nil {
		return Service{Name: "valkey", Detail: err.Error()}
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(probeTimeout))
	if _, err := conn.Write([]byte("PING\r\n")); err != nil {
		return Service{Name: "valkey", Detail: err.Error()}
	}
	buffer := make([]byte, 16)
	n, err := conn.Read(buffer)
	if err != nil || !strings.HasPrefix(string(buffer[:n]), "+PONG") {
		return Service{Name: "valkey", Detail: "no +PONG"}
	}
	return Service{Name: "valkey", OK: true}
}

func checkChromium(cfg Config) Service {
	command := exec.Command(cfg.Xdotool, "search", "--onlyvisible", "--class", "chromium")
	command.Env = append(os.Environ(), "DISPLAY="+cfg.Display)
	if err := command.Run(); err != nil {
		return Service{Name: "chromium", Detail: "no visible window"}
	}
	return Service{Name: "chromium", OK: true}
}

func checkMail(cfg Config) Service {
	info, err := os.Stat(cfg.SendmailPath)
	if err != nil {
		return Service{Name: "mail", Detail: err.Error()}
	}
	if info.Mode().Perm()&0o111 == 0 {
		return Service{Name: "mail", Detail: "sendmail is not executable"}
	}
	probe, err := os.CreateTemp(cfg.MailSpoolDir, ".health-*")
	if err != nil {
		return Service{Name: "mail", Detail: err.Error()}
	}
	name := probe.Name()
	if err := probe.Close(); err != nil {
		_ = os.Remove(name)
		return Service{Name: "mail", Detail: err.Error()}
	}
	if err := os.Remove(name); err != nil {
		return Service{Name: "mail", Detail: err.Error()}
	}
	return Service{Name: "mail", OK: true}
}

// Gather runs all probes concurrently and returns them in stable display order.
func Gather(cfg Config) Health {
	probes := []func() Service{
		func() Service { return checkX11Socket(cfg) },
		func() Service { return checkTCP("x11vnc", cfg.VNCAddr) },
		func() Service { return checkHTTP("novnc", cfg.NoVNCURL) },
		func() Service { return checkValkey(cfg.ValkeyAddr) },
		func() Service { return checkHTTP("llama", cfg.LlamaHealthURL) },
		func() Service { return checkHTTP("tts", cfg.TTSHealthURL) },
		func() Service { return checkChromium(cfg) },
		func() Service { return checkMail(cfg) },
	}
	services := make([]Service, len(probes))
	var wait sync.WaitGroup
	wait.Add(len(probes))
	for index, probe := range probes {
		go func() {
			defer wait.Done()
			services[index] = probe()
		}()
	}
	wait.Wait()

	result := Health{OK: true, Services: services}
	for _, service := range services {
		if !service.OK {
			result.OK = false
		}
	}
	return result
}
