package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientBotAPIContractAndSanitizedErrors(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.Header.Get("Content-Type"))
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":7,"is_bot":true,"first_name":"VM","username":"vm_bot"}}`))
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			var request GetUpdatesRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.Offset != 11 || request.Timeout != 30 || len(request.AllowedUpdates) != 2 {
				t.Fatalf("wrong poll request: %+v", request)
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			var request SendMessageRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.ChatID != "42" || request.Text != "hello" {
				t.Fatalf("wrong send request: %+v %v", request, err)
			}
			_, _ = w.Write([]byte(`{"ok":false,"error_code":401,"description":"FAKE_TOKEN remote secret"}`))
		case strings.HasSuffix(r.URL.Path, "/sendChatAction"):
			var request SendChatActionRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.ChatID != "42" || request.Action != "typing" {
				t.Fatalf("wrong action request: %+v %v", request, err)
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "FAKE_TOKEN", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetMe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetUpdates(context.Background(), GetUpdatesRequest{Offset: 11, Timeout: 30, AllowedUpdates: []string{"message", "edited_message"}}); err != nil {
		t.Fatal(err)
	}
	_, err = client.SendMessage(context.Background(), SendMessageRequest{ChatID: "42", Text: "hello"})
	if err == nil || strings.Contains(err.Error(), "FAKE_TOKEN") || strings.Contains(err.Error(), server.URL) || strings.Contains(err.Error(), "remote secret") {
		t.Fatalf("unsanitized error: %v", err)
	}
	if apiErr, ok := err.(*APIError); !ok || apiErr.Code != "authentication_failed" {
		t.Fatalf("wrong error: %#v", err)
	}
	if err := client.SendChatAction(context.Background(), SendChatActionRequest{ChatID: "42", Action: "typing"}); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 4 {
		t.Fatalf("calls=%v", paths)
	}
}

func TestClientErrorClassificationsAndRetryAfter(t *testing.T) {
	for status, code := range map[int]string{
		400: "protocol_error", 401: "authentication_failed", 403: "api_rejected",
		409: "poll_conflict", 429: "rate_limited", 500: "remote_unavailable",
	} {
		t.Run(code, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(fmt.Sprintf(`{"ok":false,"error_code":%d,"description":"must not escape","parameters":{"retry_after":7}}`, status)))
			}))
			defer server.Close()
			client, _ := NewClient(server.URL, "FAKE_TOKEN", server.Client())
			_, err := client.GetUpdates(context.Background(), GetUpdatesRequest{})
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.Code != code || apiErr.Status != status ||
				apiErr.RetryAfter != 7*time.Second || strings.Contains(err.Error(), "escape") {
				t.Fatalf("classification = %#v, %v", apiErr, err)
			}
		})
	}
}

func TestClientCancellationInterruptsLongPoll(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(started)
		select {
		case <-request.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	client, _ := NewClient(server.URL, "FAKE_TOKEN", server.Client())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := client.GetUpdates(ctx, GetUpdatesRequest{})
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("long poll did not cancel")
	}
}

func TestClientRejectsTrailingAndOversizedResponses(t *testing.T) {
	for name, body := range map[string]string{
		"trailing":  `{"ok":true,"result":{"id":7,"is_bot":true}} {}`,
		"oversized": strings.Repeat("x", 1024*1024+1),
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer server.Close()
			client, _ := NewClient(server.URL, "FAKE_TOKEN", server.Client())
			_, err := client.GetMe(context.Background())
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
