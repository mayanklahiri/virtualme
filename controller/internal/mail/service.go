package mail

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"html"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mayanklahiri/virtualme/controller/internal/jobs"
	"github.com/mayanklahiri/virtualme/controller/internal/valkey"
)

const (
	outboxKey = "virtualme:mail:outbox"
	outboxCap = 200
)

// Config configures outbound mail and its persistent state.
type Config struct {
	DataDir, SendmailPath     string
	Mailname, From, Smarthost string
	DKIMDomain, DKIMSelector  string
	FlushEverySec             int64
	Runner                    Runner
	Now                       func() time.Time
	Broadcast                 func([]byte)
	Key                       *rsa.PrivateKey
	Activity                  jobs.ActivityRecorder
	// Valkey persists the outbox across restarts; nil keeps it in memory.
	Valkey *valkey.Client
}

// DKIMStatus describes signing and the DNS record to publish.
type DKIMStatus struct {
	Enabled  bool   `json:"enabled"`
	Domain   string `json:"domain"`
	Selector string `json:"selector"`
	DNSName  string `json:"dnsName"`
	DNSValue string `json:"dnsValue"`
	Note     string `json:"note,omitempty"`
}

// LastResult records the last submission result.
type LastResult struct {
	TS    string `json:"ts"`
	To    string `json:"to"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// OutboxEntry is one durably tracked submission with its lifecycle status:
// queued, left_queue (dma cannot distinguish delivered from bounced),
// error, or cleared.
type OutboxEntry struct {
	ID        string `json:"id"`
	TS        int64  `json:"ts"`
	To        string `json:"to"`
	Subject   string `json:"subject"`
	Size      int    `json:"size"`
	QueueID   string `json:"queueId"`
	Status    string `json:"status"`
	LastError string `json:"lastError,omitempty"`
}

// Status is the websocket-visible mail state.
type Status struct {
	Type          string         `json:"type"`
	Mode          string         `json:"mode"`
	From          string         `json:"from"`
	Mailname      string         `json:"mailname"`
	DKIM          DKIMStatus     `json:"dkim"`
	Queue         []QueueMessage `json:"queue"`
	FlushEverySec int64          `json:"flushEverySec"`
	NextRetrySec  int64          `json:"nextRetrySec"`
	Outbox        []OutboxEntry  `json:"outbox"`
	LastResult    *LastResult    `json:"lastResult"`
}

// Service handles mail websocket requests.
type Service struct {
	config    Config
	refreshMu sync.Mutex
	mu        sync.Mutex
	last      *LastResult
	queue     []QueueMessage
	outbox    []OutboxEntry
	nextRetry int64
}

// NewService validates config and initializes DKIM state.
func NewService(config Config) (*Service, error) {
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Runner == nil {
		config.Runner = ExecRunner{}
	}
	if config.SendmailPath == "" {
		config.SendmailPath = "/usr/sbin/sendmail"
	}
	if config.Mailname == "" {
		config.Mailname, _ = os.Hostname()
	}
	if config.From == "" {
		config.From = "virtualme@" + config.Mailname
	}
	if config.DKIMSelector == "" {
		config.DKIMSelector = "virtualme"
	}
	if config.FlushEverySec <= 0 {
		config.FlushEverySec = 60
	}
	if config.DKIMDomain != "" && config.Key == nil {
		key, err := EnsureKey(KeyPath(config.DataDir))
		if err != nil {
			return nil, fmt.Errorf("initialize DKIM: %w", err)
		}
		config.Key = key
	}
	service := &Service{config: config}
	service.loadOutbox()
	return service, nil
}

func (service *Service) loadOutbox() {
	if service.config.Valkey == nil {
		return
	}
	items, err := service.config.Valkey.LRange(outboxKey, 0, -1)
	if err != nil {
		return
	}
	entries := make([]OutboxEntry, 0, len(items))
	for _, item := range items {
		var entry OutboxEntry
		if json.Unmarshal([]byte(item), &entry) == nil && entry.ID != "" {
			entries = append(entries, entry)
		}
	}
	service.mu.Lock()
	service.outbox = entries
	service.mu.Unlock()
}

// persistOutboxLocked rewrites the Valkey list; callers hold service.mu.
func (service *Service) persistOutboxLocked() {
	if len(service.outbox) > outboxCap {
		service.outbox = append([]OutboxEntry(nil), service.outbox[len(service.outbox)-outboxCap:]...)
	}
	if service.config.Valkey == nil {
		return
	}
	encoded := make([]string, 0, len(service.outbox))
	for _, entry := range service.outbox {
		if item, err := json.Marshal(entry); err == nil {
			encoded = append(encoded, string(item))
		}
	}
	_, _ = service.config.Valkey.Del(outboxKey)
	if len(encoded) > 0 {
		_, _ = service.config.Valkey.RPush(outboxKey, encoded...)
	}
}

// Handle handles mail-send and mail-status-req payloads.
func (service *Service) Handle(payload []byte, reply func([]byte) error) bool {
	var request struct {
		Type             string `json:"type"`
		ID               string `json:"id"`
		To               string `json:"to"`
		Subject          string `json:"subject"`
		Body             string `json:"body"`
		IncludeTestImage bool   `json:"includeTestImage"`
	}
	if json.Unmarshal(payload, &request) != nil ||
		(request.Type != "mail-send" && request.Type != "mail-status-req" && request.Type != "mail-clear") {
		return false
	}
	if request.Type == "mail-status-req" {
		_ = reply(service.StatusMessage())
		return true
	}
	if request.Type == "mail-clear" {
		service.ClearQueue()
		_ = reply(service.StatusMessage())
		service.broadcastStatus()
		return true
	}
	go service.send(request.ID, request.To, request.Subject, request.Body, request.IncludeTestImage, reply)
	return true
}

// ClearQueue best-effort deletes every spool pair and marks the affected
// outbox entries cleared.
func (service *Service) ClearQueue() {
	spool := filepath.Join(service.config.DataDir, "mail", "spool")
	entries, err := os.ReadDir(spool)
	if err == nil {
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || len(name) < 2 || (name[0] != 'Q' && name[0] != 'M') {
				continue
			}
			_ = os.Remove(filepath.Join(spool, name))
		}
	}
	service.mu.Lock()
	changed := false
	for index := range service.outbox {
		if service.outbox[index].Status == "queued" || service.outbox[index].Status == "error" {
			service.outbox[index].Status = "cleared"
			changed = true
		}
	}
	if changed {
		service.persistOutboxLocked()
	}
	service.mu.Unlock()
}

// spoolIDs lists the queue IDs currently present in the dma spool.
func (service *Service) spoolIDs() map[string]bool {
	result := make(map[string]bool)
	entries, err := os.ReadDir(filepath.Join(service.config.DataDir, "mail", "spool"))
	if err != nil {
		return result
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && len(name) >= 2 && name[0] == 'Q' {
			result[name[1:]] = true
		}
	}
	return result
}

func (service *Service) send(id, recipient, subject, body string, includeImage bool, reply func([]byte) error) {
	started := time.Now()
	result := map[string]any{"type": "mail-result", "id": id}
	address, err := mail.ParseAddress(strings.TrimSpace(recipient))
	if err == nil && address.Address != strings.TrimSpace(recipient) {
		err = fmt.Errorf("recipient must be a single address")
	}
	var inline []InlinePart
	htmlBody := paragraphs(body)
	if err == nil && includeImage {
		var image []byte
		image, err = TestImage()
		if err == nil {
			const cid = "img1@virtualme"
			inline = []InlinePart{{CID: cid, MIMEType: "image/png", Data: image}}
			htmlBody += `<p><img src="cid:` + cid + `" alt="Virtual Me test image"></p>`
		}
	}
	var composed []byte
	if err == nil {
		composed, err = Compose(Message{
			From: []string{service.config.From}, To: []string{address.Address},
			Subject: subject, TextBody: body, HTMLBody: htmlBody, Inline: inline,
		})
	}
	if err == nil && service.config.Key != nil {
		composed, err = Sign(composed, service.config.DKIMDomain, service.config.DKIMSelector, service.config.Key)
	}
	var before map[string]bool
	if err == nil {
		before = service.spoolIDs()
		err = Submit(context.Background(), service.config.Runner, service.config.SendmailPath,
			service.config.From, []string{address.Address}, composed)
	}
	domain := recipientDomain(address)
	if service.config.Activity != nil {
		_ = service.config.Activity.Record(jobs.ActivityEvent{
			Kind: "mail", Name: "submit", Summary: fmt.Sprintf("Submitted %d bytes to %s", len(composed), domain),
			Detail: jobs.ActivityDetail{
				DurationMS: time.Since(started).Milliseconds(), OK: err == nil,
				RecipientDomain: domain, Size: len(composed),
			},
		})
	}
	last := &LastResult{TS: service.config.Now().UTC().Format(time.RFC3339), To: recipient, OK: err == nil}
	result["ok"] = err == nil
	if err != nil {
		last.Error = err.Error()
		result["error"] = err.Error()
	}
	service.mu.Lock()
	service.last = last
	if err == nil {
		queueID := ""
		for id := range service.spoolIDs() {
			if !before[id] {
				queueID = id
				break
			}
		}
		now := service.config.Now()
		service.outbox = append(service.outbox, OutboxEntry{
			ID:      fmt.Sprintf("%d-%s", now.UnixNano(), queueID),
			TS:      now.UnixMilli(),
			To:      address.Address,
			Subject: subject,
			Size:    len(composed),
			QueueID: queueID,
			Status:  "queued",
		})
		service.persistOutboxLocked()
	}
	service.mu.Unlock()
	encoded, _ := json.Marshal(result)
	_ = reply(encoded)
	service.broadcastStatus()
}

// Start broadcasts refreshed queue state every 30 seconds while clients exist.
func (service *Service) Start(ctx context.Context, clientsConnected func() bool) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if clientsConnected == nil || clientsConnected() {
				service.broadcastStatus()
			}
		}
	}
}

func recipientDomain(address *mail.Address) string {
	if address == nil {
		return ""
	}
	_, domain, ok := strings.Cut(address.Address, "@")
	if !ok {
		return ""
	}
	return strings.ToLower(domain)
}

func paragraphs(body string) string {
	parts := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n\n")
	var output strings.Builder
	for _, part := range parts {
		output.WriteString("<p>")
		output.WriteString(strings.ReplaceAll(html.EscapeString(part), "\n", "<br>"))
		output.WriteString("</p>")
	}
	return output.String()
}

// Status returns the current status.
func (service *Service) Status() Status {
	now := service.config.Now()
	service.refresh(now)
	mode := "direct"
	if service.config.Smarthost != "" {
		mode = "smarthost"
	}
	dkim := DKIMStatus{Domain: service.config.DKIMDomain, Selector: service.config.DKIMSelector}
	if service.config.Key != nil {
		dkim.Enabled = true
		dkim.DNSName, dkim.DNSValue = DNSRecord(service.config.DKIMDomain, service.config.DKIMSelector, service.config.Key)
	} else {
		dkim.Note = "DKIM disabled; direct delivery needs SPF or DKIM alignment"
	}
	service.mu.Lock()
	last := service.last
	queue := append([]QueueMessage(nil), service.queue...)
	nextRetry := service.nextRetry
	// Newest first for display.
	outbox := make([]OutboxEntry, len(service.outbox))
	for index := range service.outbox {
		outbox[index] = service.outbox[len(service.outbox)-1-index]
	}
	service.mu.Unlock()
	return Status{Type: "mail-status", Mode: mode, From: service.config.From,
		Mailname: service.config.Mailname, DKIM: dkim, Queue: queue,
		FlushEverySec: service.config.FlushEverySec,
		NextRetrySec:  nextRetry,
		Outbox:        outbox, LastResult: last}
}

// StatusMessage returns a JSON status frame.
func (service *Service) StatusMessage() []byte {
	payload, _ := json.Marshal(service.Status())
	return payload
}

func (service *Service) broadcastStatus() {
	if service.config.Broadcast != nil {
		service.config.Broadcast(service.StatusMessage())
	}
}

func (service *Service) refresh(now time.Time) {
	service.refreshMu.Lock()
	defer service.refreshMu.Unlock()
	mailDir := filepath.Join(service.config.DataDir, "mail")
	lastFlush := readLastFlush(filepath.Join(mailDir, "last-flush"))
	nextRetry := NextRetrySec(lastFlush, service.config.FlushEverySec, now)
	queue, err := queueWithState(filepath.Join(mailDir, "spool"), filepath.Join(mailDir, "flush.log"), now, nextRetry)

	service.mu.Lock()
	defer service.mu.Unlock()
	if err != nil {
		service.nextRetry = nextRetry
		for index := range service.queue {
			service.queue[index].NextRetrySec = nextRetry
		}
		return
	}
	current := make(map[string]QueueMessage, len(queue))
	for _, message := range queue {
		current[message.ID] = message
	}
	changed := false
	for index := range service.outbox {
		entry := &service.outbox[index]
		if entry.Status != "queued" && entry.Status != "error" {
			continue
		}
		message, present := current[entry.QueueID]
		if entry.QueueID == "" || !present {
			entry.Status = "left_queue"
			changed = true
			continue
		}
		if message.LastError != "" && message.LastError != noAttemptText &&
			(entry.Status != "error" || entry.LastError != message.LastError) {
			entry.Status = "error"
			entry.LastError = message.LastError
			changed = true
		}
	}
	if changed {
		service.persistOutboxLocked()
	}
	service.queue = queue
	service.nextRetry = nextRetry
}
