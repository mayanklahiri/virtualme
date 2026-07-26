package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mayanklahiri/virtualme/controller/internal/config"
)

func TestServiceEnvironmentContainsNoSecrets(t *testing.T) {
	loaded, err := config.Load(config.Options{
		DataDir: t.TempDir(),
		Env: []string{
			"VM_MAIL_SMARTHOST=smtp.example",
			"VM_MAIL_SMARTHOST_USER=user",
			"VM_MAIL_SMARTHOST_PASS=DO_NOT_LEAK_031",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	output, err := serviceEnvironment(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(output, []byte("DO_NOT_LEAK_031")) ||
		bytes.Contains(output, []byte("VM_MAIL_SMARTHOST")) ||
		!bytes.Contains(output, []byte("VM_EFFECTIVE_LLAMA_CONTEXT='32768'")) {
		t.Fatalf("unsafe/incomplete service environment:\n%s", output)
	}
}

func TestServiceEnvironmentSplitsIPv6Addresses(t *testing.T) {
	loaded, err := config.Load(config.Options{DataDir: t.TempDir(), Env: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	loaded.Config.Desktop.VNCAddress = "[::1]:5900"
	loaded.Config.Desktop.NoVNCAddress = "[::1]:6080"
	loaded.Config.Desktop.NoVNCUpstreamAddress = "[::1]:5900"
	loaded.Config.Desktop.CDPURL = "http://[::1]:9222"
	loaded.Config.Valkey.Address = "[::1]:6379"
	loaded.Config.Llama.Address = "[::1]:8081"
	loaded.Config.TTS.Address = "[::1]:8082"
	output, err := serviceEnvironment(loaded)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"VM_EFFECTIVE_VNC_HOST='::1'",
		"VM_EFFECTIVE_VNC_PORT='5900'",
		"VM_EFFECTIVE_NOVNC_HOST='::1'",
		"VM_EFFECTIVE_CDP_HOST='::1'",
		"VM_EFFECTIVE_CDP_PORT='9222'",
		"VM_EFFECTIVE_VALKEY_HOST='::1'",
		"VM_EFFECTIVE_LLAMA_HOST='::1'",
		"VM_EFFECTIVE_TTS_HOST='::1'",
	} {
		if !bytes.Contains(output, []byte(expected)) {
			t.Fatalf("service environment missing %q:\n%s", expected, output)
		}
	}
	loaded.Config.Llama.Address = "::1:8081"
	if _, err := serviceEnvironment(loaded); err == nil {
		t.Fatal("unbracketed IPv6 address was not rejected by export")
	}
}

func TestPreflightWritesProtectedOutputs(t *testing.T) {
	root := t.TempDir()
	serviceEnv := filepath.Join(root, "run", "config.env")
	mailDir := filepath.Join(root, "mail")
	if err := preflight([]string{
		"--data-dir", root, "--service-env", serviceEnv, "--mail-dir", mailDir,
	}); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{filepath.Join(root, config.FileName), serviceEnv, filepath.Join(mailDir, "dma.conf")} {
		info, err := os.Stat(file)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode=%o", file, info.Mode().Perm())
		}
	}
}

func TestDocsGenerateAndStaleCheck(t *testing.T) {
	output := filepath.Join(t.TempDir(), "config-reference.json")
	if err := docs([]string{"--check", "--output", output}); err == nil ||
		!strings.Contains(err.Error(), "is stale") {
		t.Fatalf("missing artifact check=%v", err)
	}
	if err := docs([]string{"--output", output}); err != nil {
		t.Fatal(err)
	}
	if err := docs([]string{"--check", "--output", output}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := docs([]string{"--check", "--output", output}); err == nil {
		t.Fatal("stale artifact passed")
	}
}

func TestShellQuote(t *testing.T) {
	if got := shellQuote("a'b"); got != `'a'"'"'b'` {
		t.Fatalf("quote=%q", got)
	}
}

func TestPrepareMailSpoolMigratesWithoutDroppingQueue(t *testing.T) {
	root := t.TempDir()
	defaultPath := filepath.Join(root, "mail", "spool")
	desiredPath := filepath.Join(root, "alternate-spool")
	if err := os.MkdirAll(defaultPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(defaultPath, "queued"), []byte("message"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareMailSpool(defaultPath, desiredPath); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(defaultPath)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("default spool is not a symlink: %v %v", info, err)
	}
	if content, err := os.ReadFile(filepath.Join(desiredPath, "queued")); err != nil || string(content) != "message" {
		t.Fatalf("queued mail was not migrated: %q %v", content, err)
	}
	if err := prepareMailSpool(defaultPath, defaultPath); err != nil {
		t.Fatal(err)
	}
	info, err = os.Lstat(defaultPath)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		t.Fatalf("default spool was not restored: %v %v", info, err)
	}
	if content, err := os.ReadFile(filepath.Join(defaultPath, "queued")); err != nil || string(content) != "message" {
		t.Fatalf("queued mail was not restored: %q %v", content, err)
	}
}
