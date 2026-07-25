package chat

import (
	"encoding/json"
	"testing"

	"github.com/mayanklahiri/virtualme/controller/internal/jobs"
)

func TestTelegramMetadataAndLegacyEnvelopeCompatibility(t *testing.T) {
	message := Message{
		ID: "telegram-user:12", Role: "user", Text: "hello", Ts: 1,
		CorrelationID: "telegram:update:12",
		Source:        &Source{Channel: "telegram", ChatID: "-100", UserID: "7", UpdateID: 12},
	}
	raw, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Message
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.Source == nil || decoded.Source.ChatID != "-100" {
		t.Fatalf("message metadata lost: %+v %v", decoded, err)
	}
	var env jobs.Envelope
	if err := json.Unmarshal([]byte(`{"id":"old","type":"chat","payload":{},"priority":"interactive","enqueuedTs":1,"notBeforeTs":0,"attempts":0,"maxRetries":2,"visibilityTimeoutSec":10,"initiatorConn":"c17","projectId":"","selector":""}`), &env); err != nil {
		t.Fatal(err)
	}
	env.NormalizeLegacy()
	if env.Initiator.ID != "ws:c17" || env.Initiator.ConnectionID != "c17" || !env.Initiator.CancelOnDisconnect {
		t.Fatalf("legacy envelope not normalized: %+v", env)
	}
}

func TestDeliveryRegistrationRejectsDuplicates(t *testing.T) {
	service := New("", "", func([]byte) {})
	service.RegisterDelivery("telegram", func(_ Delivery) error { return nil })
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate delivery registration did not panic")
		}
	}()
	service.RegisterDelivery("telegram", func(_ Delivery) error { return nil })
}
