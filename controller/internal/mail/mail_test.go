package mail

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mayanklahiri/virtualme/controller/internal/jobs"
)

func fixtureMessage(t *testing.T) []byte {
	t.Helper()
	source := bytes.Repeat([]byte{0x42}, 128)
	composer := Composer{
		Now:  func() time.Time { return time.Unix(1700000000, 123).UTC() },
		Rand: bytes.NewReader(source),
	}
	message, err := composer.Compose(Message{
		From: []string{"sender@example.test"}, To: []string{"user@gmail.com"},
		Subject: "Héllo", TextBody: "plain = text\nsecond line",
		HTMLBody: `<p>html</p><img src="cid:img1@virtualme">`,
		Inline:   []InlinePart{{CID: "img1@virtualme", MIMEType: "image/png", Data: bytes.Repeat([]byte{1}, 100)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func TestComposeDeterministicRelatedMIME(t *testing.T) {
	message := fixtureMessage(t)
	if got := fmt.Sprintf("%x", sha256.Sum256(message)); got != "d329acdf89e23372fcd7d20594e798b8f981c87b1448af6e147159f574598179" {
		t.Fatalf("MIME golden digest = %s", got)
	}
	if bytes.Contains(bytes.ReplaceAll(message, []byte("\r\n"), nil), []byte("\n")) {
		t.Fatal("message contains bare LF")
	}
	text := string(message)
	for _, want := range []string{
		"multipart/related", "multipart/alternative",
		"Content-Transfer-Encoding: quoted-printable",
		"Content-ID: <img1@virtualme>",
		`cid:img1@virtualme`, "=?utf-8?q?",
	} {
		if !strings.Contains(strings.ToLower(text), strings.ToLower(want)) {
			t.Errorf("message missing %q", want)
		}
	}
	if strings.Index(text, "text/plain") > strings.Index(text, "text/html") {
		t.Fatal("HTML part precedes plain part")
	}
	for _, line := range strings.Split(text, "\r\n") {
		if isBase64Line(line) && len(line) > 76 {
			t.Fatalf("base64 line has %d columns", len(line))
		}
	}
	if !bytes.Equal(message, fixtureMessage(t)) {
		t.Fatal("injected sources did not produce byte-exact output")
	}
}

func isBase64Line(line string) bool {
	if len(line) < 20 || strings.ContainsAny(line, ":;=<> \"") {
		return false
	}
	_, err := base64.StdEncoding.DecodeString(line)
	return err == nil
}

func TestDKIMSignAndTamper(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := Sign(fixtureMessage(t), "example.test", "virtualme", key)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyDKIM(signed, &key.PublicKey); err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(signed, []byte("plain"), []byte("PLAIN"), 1)
	if err := verifyDKIM(tampered, &key.PublicKey); err == nil {
		t.Fatal("tampered body verified")
	}
}

func verifyDKIM(message []byte, public *rsa.PublicKey) error {
	headers, body, err := splitMessage(message)
	if err != nil {
		return err
	}
	dkim, ok := headerValue(headers, "dkim-signature")
	if !ok {
		return os.ErrNotExist
	}
	dkim = strings.TrimSpace(dkim)
	index := strings.LastIndex(dkim, "b=")
	signature, err := base64.StdEncoding.DecodeString(dkim[index+2:])
	if err != nil {
		return err
	}
	unsigned := dkim[:index+2]
	var canonical bytes.Buffer
	for _, name := range signedHeaders {
		value, found := headerValue(headers, name)
		if !found {
			return os.ErrNotExist
		}
		canonical.WriteString(relaxedHeader(name, value) + "\r\n")
	}
	canonical.WriteString(relaxedHeader("dkim-signature", unsigned))
	headerHash := sha256.Sum256(canonical.Bytes())
	if err := rsa.VerifyPKCS1v15(public, crypto.SHA256, headerHash[:], signature); err != nil {
		return err
	}
	bodyHash := sha256.Sum256(relaxedBody(body))
	bhStart := strings.Index(dkim, "bh=") + 3
	bhEnd := strings.Index(dkim[bhStart:], ";") + bhStart
	expected, err := base64.StdEncoding.DecodeString(dkim[bhStart:bhEnd])
	if err != nil {
		return err
	}
	if !bytes.Equal(bodyHash[:], expected) {
		return os.ErrInvalid
	}
	return nil
}

func TestEnsureKeyAndDNSRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mail", "dkim.key")
	first, err := EnsureKey(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EnsureKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if first.N.Cmp(second.N) != 0 {
		t.Fatal("key generation was not idempotent")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %o", info.Mode().Perm())
	}
	content, _ := os.ReadFile(path)
	if block, _ := pem.Decode(content); block == nil || block.Type != "RSA PRIVATE KEY" {
		t.Fatal("key is not PKCS#1 PEM")
	}
	name, value := DNSRecord("example.test", "vm", first)
	if name != "vm._domainkey.example.test" || !strings.HasPrefix(value, "v=DKIM1; k=rsa; p=") {
		t.Fatalf("DNS record = %q %q", name, value)
	}
}

type fakeRunner struct {
	path  string
	args  []string
	input []byte
	err   error
}

type fakeActivity struct {
	events chan jobs.ActivityEvent
}

func (activity *fakeActivity) Record(event jobs.ActivityEvent) error {
	activity.events <- event
	return nil
}

func (runner *fakeRunner) Run(_ context.Context, path string, args []string, input []byte) error {
	runner.path, runner.args, runner.input = path, append([]string(nil), args...), append([]byte(nil), input...)
	return runner.err
}

func TestSubmitAndQueueAndImage(t *testing.T) {
	runner := new(fakeRunner)
	message := []byte("message")
	if err := Submit(context.Background(), runner, "/sendmail", "from@test", []string{"a@test", "b@test"}, message); err != nil {
		t.Fatal(err)
	}
	if runner.path != "/sendmail" || strings.Join(runner.args, " ") != "-i -f from@test a@test b@test" ||
		!bytes.Equal(runner.input, message) {
		t.Fatalf("runner = %#v", runner)
	}
	spool := t.TempDir()
	now := time.Unix(2000, 0)
	for name, size := range map[string]int{"Qabc": 2, "Mabc": 3, "Qdef": 4} {
		if err := os.WriteFile(filepath.Join(spool, name), bytes.Repeat([]byte{'x'}, size), 0o600); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(filepath.Join(spool, name), now.Add(-10*time.Second), now.Add(-10*time.Second))
	}
	queue, err := Queue(spool, now)
	if err != nil || len(queue) != 2 || queue[0].ID != "abc" || queue[0].Size != 5 || queue[0].AgeSec != 10 {
		t.Fatalf("Queue() = %#v, %v", queue, err)
	}
	first, err := TestImage()
	if err != nil {
		t.Fatal(err)
	}
	second, _ := TestImage()
	config, err := png.DecodeConfig(bytes.NewReader(first))
	if err != nil || config.Width != 320 || config.Height != 180 || !bytes.Equal(first, second) {
		t.Fatalf("TestImage invalid or unstable: %#v, %v", config, err)
	}
}

func TestQueueCapturedDMAFixtures(t *testing.T) {
	now := time.Unix(1800000000, 0)
	queue, err := Queue("testdata", now)
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]QueueMessage)
	for _, message := range queue {
		byID[message.ID] = message
	}
	if len(queue) != 4 {
		t.Fatalf("captured queue length = %d, want 4", len(queue))
	}
	captured := byID["5.63e58bcd0ef0"]
	if captured.To != "nobody@example.com" || captured.From != "Sender <sender@example.com>" ||
		captured.Subject != "Captured dma fixture" || captured.Preview != "Captured plain body with ünicode.\r\n" {
		t.Fatalf("captured fixture = %#v", captured)
	}
	if captured.SubmittedTS != time.Unix(1700000000, 0).UnixMilli() {
		t.Fatalf("submittedTs = %d", captured.SubmittedTS)
	}
	multipart := byID["multipart"]
	if multipart.To != "first@example.com" ||
		!strings.Contains(multipart.Preview, "First plain part with ünicode and = signs.") ||
		len(multipart.Attachments) != 1 || multipart.Attachments[0].MIMEType != "application/octet-stream" ||
		multipart.Attachments[0].Size != 6 {
		t.Fatalf("multipart fixture = %#v", multipart)
	}
}

func TestQueueDefensiveParsingPreviewAndFallbacks(t *testing.T) {
	spool := t.TempDir()
	now := time.Unix(1700001000, 0)
	envelope := "unrecognized version\nSender: sender@example.test\nRecipient: first@example.test\n" +
		"Recipient: second@example.test\nUnknown: ignored\n"
	body := strings.Repeat("界", 510)
	message := "From: Fallback <header@example.test>\r\nTo: ignored@example.test\r\n" +
		"Subject: =?UTF-8?Q?fallback_=C3=BC?=\r\nContent-Type: text/plain\r\n\r\n" + body
	for name, content := range map[string]string{"Qdefensive": envelope, "Mdefensive": message} {
		path := filepath.Join(spool, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(path, now.Add(-20*time.Second), now.Add(-20*time.Second))
	}
	if err := os.WriteFile(filepath.Join(spool, "Qmalformed"), []byte{0xff, 0, '\n'}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(spool, "Morphan"), []byte("Subject: not queued\n\nbody"), 0o600); err != nil {
		t.Fatal(err)
	}
	queue, err := Queue(spool, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(queue) != 2 {
		t.Fatalf("queue = %#v", queue)
	}
	item := queue[0]
	if item.ID != "defensive" || item.To != "first@example.test (+1 more)" ||
		len(item.Recipients) != 2 || item.Subject != "fallback ü" ||
		len([]rune(item.Preview)) != previewRunes || item.SubmittedTS != now.Add(-20*time.Second).UnixMilli() {
		t.Fatalf("defensive item = %#v", item)
	}
}

func TestQueueErrorsAndRetryMath(t *testing.T) {
	spool := t.TempDir()
	if err := os.WriteFile(filepath.Join(spool, "Qabc"),
		[]byte("Sender: a@example.com\nRecipient: b@example.com\nError: envelope diagnostic\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(spool, "Mabc"), []byte("Subject: test\n\nbody"), 0o600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "flush.log")
	if err := os.WriteFile(logPath, []byte(
		"dma xabc: wrong message\ndma abc: first failure\ndma abc-extra: wrong message\n"+
			"dma abc: connection refused\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1060, 0)
	queue, err := queueWithState(spool, logPath, now, 42)
	if err != nil || len(queue) != 1 || queue[0].LastError != "envelope diagnostic" ||
		queue[0].NextRetrySec != 42 {
		t.Fatalf("queue = %#v, %v", queue, err)
	}
	if err := os.WriteFile(filepath.Join(spool, "Qabc"),
		[]byte("Sender: a@example.com\nRecipient: b@example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	queue, err = queueWithState(spool, logPath, now, 42)
	if err != nil || queue[0].LastError != "dma abc: connection refused" {
		t.Fatalf("log fallback queue = %#v, %v", queue, err)
	}
	if got := NextRetrySec(time.Unix(1000, 0), 60, time.Unix(1018, 0)); got != 42 {
		t.Fatalf("NextRetrySec = %d", got)
	}
	if got := NextRetrySec(time.Unix(1000, 0), 60, time.Unix(1100, 0)); got != 0 {
		t.Fatalf("expired NextRetrySec = %d", got)
	}
}

// spoolingRunner simulates dma accepting a submission by writing a
// queue/message pair into the spool, like the real binary does.
type spoolingRunner struct {
	spool string
	id    string
	err   error
}

func (runner *spoolingRunner) Run(_ context.Context, _ string, _ []string, input []byte) error {
	if runner.err != nil {
		return runner.err
	}
	envelope := "Sender: sender@example.test\nRecipient: user@example.test\n"
	if err := os.WriteFile(filepath.Join(runner.spool, "Q"+runner.id), []byte(envelope), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(runner.spool, "M"+runner.id), input, 0o600)
}

func newOutboxService(t *testing.T, runner Runner, now func() time.Time) (*Service, string) {
	t.Helper()
	dataDir := t.TempDir()
	spool := filepath.Join(dataDir, "mail", "spool")
	if err := os.MkdirAll(spool, 0o700); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(Config{
		DataDir: dataDir, SendmailPath: "/sendmail", Mailname: "example.test",
		From: "sender@example.test", Smarthost: "relay", Runner: runner, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, spool
}

func sendAndWait(t *testing.T, service *Service, id string) {
	t.Helper()
	replies := make(chan []byte, 1)
	payload := fmt.Sprintf(`{"type":"mail-send","id":%q,"to":"user@example.test","subject":"test","body":"hello"}`, id)
	if !service.Handle([]byte(payload), func(reply []byte) error { replies <- reply; return nil }) {
		t.Fatal("mail-send not handled")
	}
	<-replies
}

func TestOutboxTracksQueuedErrorAndLeftQueue(t *testing.T) {
	now := time.Unix(1700000000, 0)
	var service *Service
	var spool string
	runner := &spoolingRunner{id: "abc"}
	service, spool = newOutboxService(t, runner, func() time.Time { return now })
	runner.spool = spool
	sendAndWait(t, service, "m1")

	status := service.Status()
	if len(status.Outbox) != 1 {
		t.Fatalf("outbox = %#v", status.Outbox)
	}
	entry := status.Outbox[0]
	if entry.Status != "queued" || entry.QueueID != "abc" ||
		entry.To != "user@example.test" || entry.Subject != "test" || entry.Size <= 0 {
		t.Fatalf("queued entry = %#v", entry)
	}

	// dma records a delivery error in the envelope: entry becomes error.
	envelope := "Sender: sender@example.test\nRecipient: user@example.test\nError: relay refused\n"
	if err := os.WriteFile(filepath.Join(spool, "Qabc"), []byte(envelope), 0o600); err != nil {
		t.Fatal(err)
	}
	status = service.Status()
	if status.Outbox[0].Status != "error" || !strings.Contains(status.Outbox[0].LastError, "relay refused") {
		t.Fatalf("error entry = %#v", status.Outbox[0])
	}

	// The pair vanishing from the spool means dma delivered or bounced it.
	for _, name := range []string{"Qabc", "Mabc"} {
		if err := os.Remove(filepath.Join(spool, name)); err != nil {
			t.Fatal(err)
		}
	}
	status = service.Status()
	if status.Outbox[0].Status != "left_queue" {
		t.Fatalf("left_queue entry = %#v", status.Outbox[0])
	}

	// No session timeline noise survives the outbox migration.
	if encoded := string(service.StatusMessage()); strings.Contains(encoded, "Flush ran") ||
		strings.Contains(encoded, "timeline") {
		t.Fatalf("timeline noise in status: %s", encoded)
	}
}

func TestClearQueueRemovesSpoolAndMarksOutbox(t *testing.T) {
	now := time.Unix(1700000000, 0)
	runner := &spoolingRunner{id: "abc"}
	service, spool := newOutboxService(t, runner, func() time.Time { return now })
	runner.spool = spool
	sendAndWait(t, service, "m1")
	// A second, orphan pair placed by an external submitter.
	for name, content := range map[string]string{
		"Qzzz": "Sender: sender@example.test\nRecipient: other@example.test\n",
		"Mzzz": "Subject: orphan\n\nbody",
	} {
		if err := os.WriteFile(filepath.Join(spool, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	replies := make(chan []byte, 1)
	if !service.Handle([]byte(`{"type":"mail-clear"}`), func(reply []byte) error { replies <- reply; return nil }) {
		t.Fatal("mail-clear not handled")
	}
	<-replies
	entries, err := os.ReadDir(spool)
	if err != nil || len(entries) != 0 {
		t.Fatalf("spool after clear = %v, %v", entries, err)
	}
	status := service.Status()
	if len(status.Outbox) != 1 || status.Outbox[0].Status != "cleared" {
		t.Fatalf("outbox after clear = %#v", status.Outbox)
	}
	if len(status.Queue) != 0 {
		t.Fatalf("queue after clear = %#v", status.Queue)
	}
}

func TestServiceFrames(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "mail", "spool"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := new(fakeRunner)
	activity := &fakeActivity{events: make(chan jobs.ActivityEvent, 1)}
	service, err := NewService(Config{
		DataDir: dataDir, SendmailPath: "/sendmail", Mailname: "example.test",
		From: "virtualme@example.test", Smarthost: "relay", Runner: runner,
		Now:      func() time.Time { return time.Unix(1700000000, 0) },
		Activity: activity,
	})
	if err != nil {
		t.Fatal(err)
	}
	replies := make(chan []byte, 2)
	write := func(payload []byte) error { replies <- payload; return nil }
	if !service.Handle([]byte(`{"type":"mail-send","id":"x","to":"user@example.test","subject":"test","body":"hello","includeTestImage":true}`), write) {
		t.Fatal("mail-send not handled")
	}
	result := <-replies
	if !strings.Contains(string(result), `"ok":true`) || !bytes.Contains(runner.input, []byte("multipart/related")) {
		t.Fatalf("result=%s input=%s", result, runner.input)
	}
	event := <-activity.events
	encodedEvent, _ := json.Marshal(event)
	if event.Detail.RecipientDomain != "example.test" || bytes.Contains(encodedEvent, []byte("user@example.test")) {
		t.Fatalf("mail activity leaked recipient: %s", encodedEvent)
	}
	if !service.Handle([]byte(`{"type":"mail-status-req"}`), write) {
		t.Fatal("status request not handled")
	}
	status := string(<-replies)
	if !strings.Contains(status, `"mode":"smarthost"`) ||
		!strings.Contains(status, `"flushEverySec":60`) ||
		!strings.Contains(status, `"outbox"`) ||
		!strings.Contains(status, `"lastResult"`) {
		t.Fatalf("status=%s", status)
	}
}
