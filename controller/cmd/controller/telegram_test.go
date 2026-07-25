package main

import "testing"

func TestTelegramAPIBaseGuard(t *testing.T) {
	t.Setenv("VM_TELEGRAM_TEST_MODE", "")
	t.Setenv("VM_TELEGRAM_API_BASE_URL", "")
	if got, err := telegramAPIBase(); err != nil || got != "https://api.telegram.org" {
		t.Fatalf("production base: %q %v", got, err)
	}
	for _, base := range []string{"http://127.0.0.1:9000", "http://localhost:9000", "http://vmhost:9000"} {
		t.Setenv("VM_TELEGRAM_TEST_MODE", "1")
		t.Setenv("VM_TELEGRAM_API_BASE_URL", base)
		if got, err := telegramAPIBase(); err != nil || got != base {
			t.Fatalf("test base %q: %q %v", base, got, err)
		}
	}
	for _, base := range []string{"https://vmhost/x", "http://example.com", "http://user@vmhost", "http://vmhost?token=x"} {
		t.Setenv("VM_TELEGRAM_TEST_MODE", "1")
		t.Setenv("VM_TELEGRAM_API_BASE_URL", base)
		if _, err := telegramAPIBase(); err == nil {
			t.Fatalf("unsafe base accepted: %s", base)
		}
	}
}
