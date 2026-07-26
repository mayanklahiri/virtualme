package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTelegramSchemaAndSecretOnlyStorage(t *testing.T) {
	schema, err := EmbeddedSchema()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := schema.Defaults()
	telegram := raw["integrations"].(map[string]any)["telegram"].(map[string]any)
	telegram["enabled"] = true
	telegram["allowedChatIds"] = []any{"-100", "42"}
	telegram["botToken"] = "literal-secret"
	if err := schema.Validate(raw); err == nil || !strings.Contains(err.Error(), "botToken") {
		t.Fatalf("literal token accepted: %v", err)
	}
	telegram["botToken"] = "${env:VM_TEST_TELEGRAM_TOKEN}"
	if err := schema.Validate(raw); err != nil {
		t.Fatal(err)
	}
	telegram["botToken"] = "${file:/home/virtualme/.virtualme/telegram-token}"
	if err := schema.Validate(raw); err != nil {
		t.Fatalf("file reference rejected: %v", err)
	}
	telegram["botToken"] = "${env:VM_TEST_TELEGRAM_TOKEN}"

	dataDir := t.TempDir()
	yaml, _ := schema.Emit(raw)
	if err := os.WriteFile(filepath.Join(dataDir, FileName), yaml, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(Options{DataDir: dataDir, Env: []string{"VM_TEST_TELEGRAM_TOKEN=obviously-fake-runtime-token"}})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Config.Integrations.Telegram.BotToken != "obviously-fake-runtime-token" {
		t.Fatal("effective token was not resolved")
	}
	rawToken := loaded.Raw["integrations"].(map[string]any)["telegram"].(map[string]any)["botToken"]
	if rawToken != "${env:VM_TEST_TELEGRAM_TOKEN}" {
		t.Fatalf("raw token reference changed: %#v", rawToken)
	}
	persisted, _ := os.ReadFile(filepath.Join(dataDir, FileName))
	if strings.Contains(string(persisted), "obviously-fake-runtime-token") {
		t.Fatal("resolved token leaked to persisted config")
	}
}

func TestTelegramUnavailableSecretKeepsControllerConfigLoadable(t *testing.T) {
	schema, err := EmbeddedSchema()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := schema.Defaults()
	telegram := raw["integrations"].(map[string]any)["telegram"].(map[string]any)
	telegram["enabled"] = true
	telegram["allowedChatIds"] = []any{"42"}
	telegram["botToken"] = "${env:VM_MISSING_TELEGRAM_TOKEN}"
	dataDir := t.TempDir()
	yaml, _ := schema.Emit(raw)
	if err := os.WriteFile(filepath.Join(dataDir, FileName), yaml, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(Options{DataDir: dataDir, Env: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Config.Integrations.Telegram.BotToken != "" ||
		loaded.Secrets["integrations.telegram.botToken"].Error != "secret_unavailable" {
		t.Fatalf("unavailable secret state = %+v", loaded.Secrets["integrations.telegram.botToken"])
	}
}
