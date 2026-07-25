package mail

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	stdmail "net/mail"
	"net/textproto"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	previewRunes = 500
	maxMIMEDepth = 16
	// noAttemptText marks queue entries dma has not tried to deliver yet;
	// it is informational, not a delivery error.
	noAttemptText = "no delivery attempt recorded yet"
)

// Attachment describes a non-body MIME part without exposing its contents.
type Attachment struct {
	MIMEType string `json:"mimeType"`
	Size     int64  `json:"size"`
}

// QueueMessage describes one queued dma message.
type QueueMessage struct {
	ID           string       `json:"id"`
	Size         int64        `json:"size"`
	AgeSec       int64        `json:"ageSec"`
	To           string       `json:"to"`
	Recipients   []string     `json:"recipients"`
	Subject      string       `json:"subject"`
	From         string       `json:"from"`
	SubmittedTS  int64        `json:"submittedTs"`
	Preview      string       `json:"preview"`
	Attachments  []Attachment `json:"attachments"`
	LastError    string       `json:"lastError"`
	NextRetrySec int64        `json:"nextRetrySec"`
}

type spoolMessage struct {
	id, sender, envelopeError string
	recipients                []string
	size                      int64
	mod                       time.Time
	hasEnvelope               bool
}

// Queue parses dma queue/message pairs. Individual malformed or unreadable
// files yield partial entries rather than making the whole queue unavailable.
func Queue(directory string, now time.Time) ([]QueueMessage, error) {
	return queueWithState(directory, "", now, 0)
}

func queueWithState(directory, flushLog string, now time.Time, nextRetrySec int64) ([]QueueMessage, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	grouped := make(map[string]*spoolMessage)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || len(name) < 2 || (name[0] != 'M' && name[0] != 'Q') {
			continue
		}
		id := name[1:]
		item := grouped[id]
		if item == nil {
			item = &spoolMessage{id: id}
			grouped[id] = item
		}
		if info, infoErr := entry.Info(); infoErr == nil {
			item.size += info.Size()
			if item.mod.IsZero() || info.ModTime().Before(item.mod) {
				item.mod = info.ModTime()
			}
		}
		if name[0] == 'Q' {
			item.hasEnvelope = true
			parseEnvelope(filepath.Join(directory, name), item)
		}
	}

	logErrors := parseFlushLog(flushLog, grouped)
	result := make([]QueueMessage, 0, len(grouped))
	for id, item := range grouped {
		// The Q file is authoritative queue state. dma can remove or rename
		// either half of a pair while this read-only snapshot is in progress.
		if !item.hasEnvelope {
			continue
		}
		message := QueueMessage{
			ID: id, Size: item.size, Recipients: append([]string(nil), item.recipients...),
			From: item.sender, Attachments: []Attachment{}, NextRetrySec: nextRetrySec,
		}
		if !item.mod.IsZero() {
			age := now.Sub(item.mod).Seconds()
			if age > 0 {
				message.AgeSec = int64(age)
			}
			message.SubmittedTS = item.mod.UnixMilli()
		}
		parseMessage(filepath.Join(directory, "M"+id), &message)
		if len(message.Recipients) == 0 {
			message.Recipients = headerAddresses(message.To)
		}
		message.To = summarizeRecipients(message.Recipients)
		message.LastError = item.envelopeError
		if message.LastError == "" {
			message.LastError = logErrors[id]
		}
		if message.LastError == "" {
			message.LastError = noAttemptText
		}
		result = append(result, message)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func parseEnvelope(path string, item *spoolMessage) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()
	scanner := bufio.NewScanner(io.LimitReader(file, 1024*1024))
	scanner.Buffer(make([]byte, 1024), 64*1024)
	var bare []string
	for scanner.Scan() {
		line := strings.TrimSpace(strings.ToValidUTF8(scanner.Text(), "�"))
		if line == "" {
			continue
		}
		key, value, found := strings.Cut(line, ":")
		if !found {
			bare = append(bare, line)
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "sender", "from", "envelope-from":
			item.sender = cleanAddress(value)
		case "recipient", "to", "envelope-to":
			if address := cleanAddress(value); address != "" {
				item.recipients = appendUnique(item.recipients, address)
			}
		case "error", "last-error", "status", "diagnostic", "reason":
			if value != "" {
				item.envelopeError = value
			}
		}
	}
	if scanner.Err() != nil {
		return
	}
	// Older dma variants use positional envelope lines. Ignore version/ID
	// lines and accept only values that parse as addresses.
	for _, line := range bare {
		address := cleanAddress(line)
		if address == "" || !strings.Contains(address, "@") {
			continue
		}
		if item.sender == "" {
			item.sender = address
		} else {
			item.recipients = appendUnique(item.recipients, address)
		}
	}
}

func parseMessage(path string, result *QueueMessage) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()
	info, _ := file.Stat()
	// Controller submissions are small, but bound a damaged or externally
	// populated spool file so status requests cannot consume arbitrary memory.
	message, err := stdmail.ReadMessage(io.LimitReader(file, 2*1024*1024))
	if err != nil {
		return
	}
	result.Subject = decodeHeader(message.Header.Get("Subject"))
	if from := decodeHeader(message.Header.Get("From")); from != "" {
		result.From = from
	}
	result.To = decodeHeader(message.Header.Get("To"))
	if date, dateErr := message.Header.Date(); dateErr == nil {
		result.SubmittedTS = date.UnixMilli()
	} else if info != nil {
		result.SubmittedTS = info.ModTime().UnixMilli()
	}
	preview, attachments := inspectPart(textproto.MIMEHeader(message.Header), message.Body, false, 0)
	result.Preview = capPreview(preview)
	result.Attachments = attachments
}

func inspectPart(header textproto.MIMEHeader, body io.Reader, attachment bool, depth int) (string, []Attachment) {
	if depth > maxMIMEDepth {
		return "", nil
	}
	contentType := header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType == "" {
		mediaType = "text/plain"
	}
	disposition, _, _ := mime.ParseMediaType(header.Get("Content-Disposition"))
	attachment = attachment || strings.EqualFold(disposition, "attachment")
	if strings.HasPrefix(strings.ToLower(mediaType), "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return "", nil
		}
		reader := multipart.NewReader(body, boundary)
		var preview string
		var attachments []Attachment
		for {
			part, partErr := reader.NextPart()
			if partErr == io.EOF {
				break
			}
			if partErr != nil {
				break
			}
			partPreview, partAttachments := inspectPart(part.Header, part, attachment, depth+1)
			if preview == "" {
				preview = partPreview
			}
			attachments = append(attachments, partAttachments...)
			_ = part.Close()
		}
		return preview, attachments
	}
	decoded := decodeTransfer(header.Get("Content-Transfer-Encoding"), body)
	if strings.EqualFold(mediaType, "text/plain") && !attachment {
		content, _ := io.ReadAll(io.LimitReader(decoded, 64*1024))
		return string(content), nil
	}
	if strings.HasPrefix(strings.ToLower(mediaType), "text/") && !attachment {
		_, _ = io.Copy(io.Discard, io.LimitReader(decoded, 128*1024*1024))
		return "", nil
	}
	size, _ := io.Copy(io.Discard, io.LimitReader(decoded, 128*1024*1024))
	return "", []Attachment{{MIMEType: mediaType, Size: size}}
}

func decodeTransfer(encoding string, body io.Reader) io.Reader {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, body)
	case "quoted-printable":
		return quotedprintable.NewReader(body)
	default:
		return body
	}
}

func parseFlushLog(path string, queue map[string]*spoolMessage) map[string]string {
	result := make(map[string]string)
	if path == "" || len(queue) == 0 {
		return result
	}
	file, err := os.Open(path)
	if err != nil {
		return result
	}
	defer file.Close()
	lines := make([]string, 0, 500)
	scanner := bufio.NewScanner(io.LimitReader(file, 8*1024*1024))
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(strings.ToValidUTF8(scanner.Text(), "�"))
		if len(lines) == 500 {
			copy(lines, lines[1:])
			lines[499] = line
		} else {
			lines = append(lines, line)
		}
	}
	if scanner.Err() != nil {
		return result
	}
	for _, line := range lines {
		if line == "" {
			continue
		}
		for id := range queue {
			if containsQueueID(line, id) {
				result[id] = line
			}
		}
	}
	return result
}

func containsQueueID(line, id string) bool {
	for offset := 0; ; {
		index := strings.Index(line[offset:], id)
		if index < 0 {
			return false
		}
		index += offset
		beforeOK := index == 0 || !isQueueIDByte(line[index-1])
		end := index + len(id)
		afterOK := end == len(line) || !isQueueIDByte(line[end])
		if beforeOK && afterOK {
			return true
		}
		offset = index + 1
	}
}

func isQueueIDByte(value byte) bool {
	return value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9' ||
		value == '.' || value == '_' || value == '-'
}

func cleanAddress(value string) string {
	value = strings.TrimSpace(strings.Trim(value, "<>"))
	if address, err := stdmail.ParseAddress(value); err == nil {
		return address.Address
	}
	return ""
}

func headerAddresses(value string) []string {
	addresses, err := stdmail.ParseAddressList(value)
	if err != nil {
		return nil
	}
	result := make([]string, 0, len(addresses))
	for _, address := range addresses {
		result = appendUnique(result, address.Address)
	}
	return result
}

func appendUnique(values []string, value string) []string {
	for _, current := range values {
		if current == value {
			return values
		}
	}
	return append(values, value)
}

func summarizeRecipients(recipients []string) string {
	if len(recipients) == 0 {
		return ""
	}
	if len(recipients) == 1 {
		return recipients[0]
	}
	return fmt.Sprintf("%s (+%d more)", recipients[0], len(recipients)-1)
}

func decodeHeader(value string) string {
	decoded, err := new(mime.WordDecoder).DecodeHeader(value)
	if err == nil {
		value = decoded
	}
	return strings.ToValidUTF8(value, "�")
}

func capPreview(value string) string {
	value = strings.ToValidUTF8(strings.ReplaceAll(value, "\x00", ""), "�")
	if utf8.RuneCountInString(value) <= previewRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:previewRunes])
}

// NextRetrySec returns seconds until the next queue flush marker interval.
func NextRetrySec(lastFlush time.Time, flushEverySec int64, now time.Time) int64 {
	if lastFlush.IsZero() || flushEverySec <= 0 {
		return 0
	}
	seconds := lastFlush.Add(time.Duration(flushEverySec) * time.Second).Sub(now).Seconds()
	if seconds <= 0 {
		return 0
	}
	return int64(seconds)
}

func readLastFlush(path string) time.Time {
	content, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}
	}
	seconds, err := strconv.ParseInt(strings.TrimSpace(string(content)), 10, 64)
	if err != nil || seconds <= 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0)
}
