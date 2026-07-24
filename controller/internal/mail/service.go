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
)

// Config configures outbound mail and its persistent state.
type Config struct {
	DataDir, SendmailPath     string
	Mailname, From, Smarthost string
	DKIMDomain, DKIMSelector  string
	Runner                    Runner
	Now                       func() time.Time
	Broadcast                 func([]byte)
	Key                       *rsa.PrivateKey
	Activity                  jobs.ActivityRecorder
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

// Status is the websocket-visible mail state.
type Status struct {
	Type       string       `json:"type"`
	Mode       string       `json:"mode"`
	From       string       `json:"from"`
	Mailname   string       `json:"mailname"`
	DKIM       DKIMStatus   `json:"dkim"`
	Queue      []QueueEntry `json:"queue"`
	LastResult *LastResult  `json:"lastResult"`
}

// Service handles mail websocket requests.
type Service struct {
	config Config
	mu     sync.Mutex
	last   *LastResult
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
	if config.DKIMDomain != "" && config.Key == nil {
		key, err := EnsureKey(KeyPath(config.DataDir))
		if err != nil {
			return nil, fmt.Errorf("initialize DKIM: %w", err)
		}
		config.Key = key
	}
	return &Service{config: config}, nil
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
		(request.Type != "mail-send" && request.Type != "mail-status-req") {
		return false
	}
	if request.Type == "mail-status-req" {
		_ = reply(service.StatusMessage())
		return true
	}
	go service.send(request.ID, request.To, request.Subject, request.Body, request.IncludeTestImage, reply)
	return true
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
	if err == nil {
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
	service.mu.Unlock()
	encoded, _ := json.Marshal(result)
	_ = reply(encoded)
	service.broadcastStatus()
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
	queue, err := Queue(filepath.Join(service.config.DataDir, "mail", "spool"), service.config.Now())
	if err != nil {
		queue = []QueueEntry{}
	}
	service.mu.Lock()
	last := service.last
	service.mu.Unlock()
	return Status{Type: "mail-status", Mode: mode, From: service.config.From,
		Mailname: service.config.Mailname, DKIM: dkim, Queue: queue, LastResult: last}
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
