// Package mail composes, signs, submits, and inspects outbound mail.
package mail

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	stdmail "net/mail"
	"net/textproto"
	"strings"
	"time"
)

// InlinePart is a MIME part referenced by CID from HTMLBody.
type InlinePart struct {
	CID      string
	MIMEType string
	Data     []byte
}

// Message is an outbound RFC 5322 message.
type Message struct {
	From, To           []string
	Subject            string
	TextBody, HTMLBody string
	Inline             []InlinePart
}

// Composer permits deterministic clocks and randomness in tests.
type Composer struct {
	Now  func() time.Time
	Rand io.Reader
}

// Compose creates a multipart/related message.
func Compose(message Message) ([]byte, error) {
	return (Composer{Now: time.Now, Rand: rand.Reader}).Compose(message)
}

// Compose creates a multipart/related message using the configured sources.
func (c Composer) Compose(message Message) ([]byte, error) {
	if len(message.From) == 0 || len(message.To) == 0 {
		return nil, errors.New("from and to are required")
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Rand == nil {
		c.Rand = rand.Reader
	}
	related, err := c.token(18)
	if err != nil {
		return nil, err
	}
	alternative, err := c.token(18)
	if err != nil {
		return nil, err
	}
	messageToken, err := c.token(8)
	if err != nil {
		return nil, err
	}
	mailname := addressDomain(message.From[0])
	if mailname == "" {
		mailname = "virtualme.local"
	}
	now := c.Now()
	from, err := encodeAddresses(message.From)
	if err != nil {
		return nil, fmt.Errorf("from: %w", err)
	}
	to, err := encodeAddresses(message.To)
	if err != nil {
		return nil, fmt.Errorf("to: %w", err)
	}
	var output bytes.Buffer
	headers := []struct{ name, value string }{
		{"From", from},
		{"To", to},
		{"Subject", encodeHeader(message.Subject)},
		{"Date", now.Format(time.RFC1123Z)},
		{"Message-ID", fmt.Sprintf("<%d.%s@%s>", now.UnixNano(), messageToken, mailname)},
		{"MIME-Version", "1.0"},
		{"Content-Type", fmt.Sprintf(`multipart/related; type="multipart/alternative"; boundary="%s"`, related)},
	}
	for _, header := range headers {
		if hasNewline(header.value) {
			return nil, fmt.Errorf("%s contains a newline", header.name)
		}
		fmt.Fprintf(&output, "%s: %s\r\n", header.name, header.value)
	}
	output.WriteString("\r\n")

	relatedWriter := multipart.NewWriter(&output)
	if err := relatedWriter.SetBoundary(related); err != nil {
		return nil, err
	}
	alternativeHeader := textproto.MIMEHeader{}
	alternativeHeader.Set("Content-Type", fmt.Sprintf(`multipart/alternative; boundary="%s"`, alternative))
	part, err := relatedWriter.CreatePart(alternativeHeader)
	if err != nil {
		return nil, err
	}
	alternativeWriter := multipart.NewWriter(part)
	if err := alternativeWriter.SetBoundary(alternative); err != nil {
		return nil, err
	}
	if err := writeTextPart(alternativeWriter, "text/plain; charset=utf-8", message.TextBody); err != nil {
		return nil, err
	}
	if err := writeTextPart(alternativeWriter, "text/html; charset=utf-8", message.HTMLBody); err != nil {
		return nil, err
	}
	if err := alternativeWriter.Close(); err != nil {
		return nil, err
	}
	for index, inline := range message.Inline {
		if hasNewline(inline.CID) || hasNewline(inline.MIMEType) || inline.CID == "" {
			return nil, errors.New("invalid inline part")
		}
		contentType := inline.MIMEType
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		header := textproto.MIMEHeader{}
		header.Set("Content-Type", contentType)
		header.Set("Content-Transfer-Encoding", "base64")
		header.Set("Content-ID", "<"+strings.Trim(inline.CID, "<>")+">")
		header.Set("Content-Disposition", fmt.Sprintf(`inline; filename="inline-%d%s"`, index+1, extension(contentType)))
		part, err := relatedWriter.CreatePart(header)
		if err != nil {
			return nil, err
		}
		writeBase64(part, inline.Data)
	}
	if err := relatedWriter.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func writeTextPart(writer *multipart.Writer, contentType, body string) error {
	header := textproto.MIMEHeader{}
	header.Set("Content-Type", contentType)
	header.Set("Content-Transfer-Encoding", "quoted-printable")
	part, err := writer.CreatePart(header)
	if err != nil {
		return err
	}
	qp := quotedprintable.NewWriter(part)
	if _, err := io.WriteString(qp, normalizeCRLF(body)); err != nil {
		return err
	}
	return qp.Close()
}

func writeBase64(writer io.Writer, data []byte) {
	encoded := base64.StdEncoding.EncodeToString(data)
	for len(encoded) > 76 {
		fmt.Fprintf(writer, "%s\r\n", encoded[:76])
		encoded = encoded[76:]
	}
	if encoded != "" {
		fmt.Fprintf(writer, "%s\r\n", encoded)
	}
}

func (c Composer) token(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := io.ReadFull(c.Rand, raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func encodeHeader(value string) string {
	for _, char := range value {
		if char > 127 {
			return mime.QEncoding.Encode("utf-8", value)
		}
	}
	return value
}

func encodeAddresses(values []string) (string, error) {
	encoded := make([]string, 0, len(values))
	for _, value := range values {
		address, err := stdmail.ParseAddress(value)
		if err != nil {
			return "", err
		}
		encoded = append(encoded, address.String())
	}
	return strings.Join(encoded, ", "), nil
}

func normalizeCRLF(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\n", "\r\n")
}

func hasNewline(value string) bool { return strings.ContainsAny(value, "\r\n") }

func addressDomain(address string) string {
	at := strings.LastIndex(address, "@")
	if at < 0 {
		return ""
	}
	return strings.Trim(strings.TrimSpace(address[at+1:]), ">")
}

func extension(contentType string) string {
	if contentType == "image/png" {
		return ".png"
	}
	return ""
}
