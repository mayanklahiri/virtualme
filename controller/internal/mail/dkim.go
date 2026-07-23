package mail

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var signedHeaders = []string{"from", "to", "subject", "date", "message-id", "mime-version", "content-type"}

// EnsureKey loads an existing PKCS#1 key or atomically creates a new 2048-bit key.
func EnsureKey(path string) (*rsa.PrivateKey, error) {
	if content, err := os.ReadFile(path); err == nil {
		block, _ := pem.Decode(content)
		if block == nil {
			return nil, errors.New("invalid DKIM PEM")
		}
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	content := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, content, 0o600); err != nil {
		return nil, err
	}
	if err := os.Chmod(temporary, 0o600); err != nil {
		_ = os.Remove(temporary)
		return nil, err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return nil, err
	}
	return key, nil
}

// DNSRecord returns the DKIM TXT owner and value for key.
func DNSRecord(domain, selector string, key *rsa.PrivateKey) (string, string) {
	publicDER := x509.MarshalPKCS1PublicKey(&key.PublicKey)
	return selector + "._domainkey." + domain,
		"v=DKIM1; k=rsa; p=" + base64.StdEncoding.EncodeToString(publicDER)
}

// Sign prepends an RFC 6376 rsa-sha256 relaxed/relaxed DKIM signature.
func Sign(message []byte, domain, selector string, key *rsa.PrivateKey) ([]byte, error) {
	headers, body, err := splitMessage(message)
	if err != nil {
		return nil, err
	}
	canonicalBody := relaxedBody(body)
	bodyDigest := sha256.Sum256(canonicalBody)
	value := fmt.Sprintf("v=1; a=rsa-sha256; c=relaxed/relaxed; d=%s; s=%s; h=%s; bh=%s; b=",
		domain, selector, strings.Join(signedHeaders, ":"),
		base64.StdEncoding.EncodeToString(bodyDigest[:]))
	var signing bytes.Buffer
	for _, name := range signedHeaders {
		raw, ok := headerValue(headers, name)
		if !ok {
			return nil, fmt.Errorf("missing signed header %s", name)
		}
		signing.WriteString(relaxedHeader(name, raw))
		signing.WriteString("\r\n")
	}
	signing.WriteString(relaxedHeader("dkim-signature", value))
	digest := sha256.Sum256(signing.Bytes())
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return nil, err
	}
	var result bytes.Buffer
	result.WriteString("DKIM-Signature: ")
	result.WriteString(value)
	result.WriteString(base64.StdEncoding.EncodeToString(signature))
	result.WriteString("\r\n")
	result.Write(message)
	return result.Bytes(), nil
}

type parsedHeader struct {
	name  string
	value string
}

func splitMessage(message []byte) ([]parsedHeader, []byte, error) {
	index := bytes.Index(message, []byte("\r\n\r\n"))
	if index < 0 {
		return nil, nil, errors.New("message has no CRLF header separator")
	}
	lines := strings.Split(string(message[:index]), "\r\n")
	headers := make([]parsedHeader, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			if len(headers) == 0 {
				return nil, nil, errors.New("orphan folded header")
			}
			headers[len(headers)-1].value += "\r\n" + line
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon <= 0 {
			return nil, nil, errors.New("malformed header")
		}
		headers = append(headers, parsedHeader{name: strings.ToLower(line[:colon]), value: line[colon+1:]})
	}
	return headers, message[index+4:], nil
}

func headerValue(headers []parsedHeader, name string) (string, bool) {
	for index := len(headers) - 1; index >= 0; index-- {
		if headers[index].name == name {
			return headers[index].value, true
		}
	}
	return "", false
}

func relaxedHeader(name, value string) string {
	value = strings.ReplaceAll(value, "\r\n", "")
	value = strings.Join(strings.Fields(value), " ")
	return strings.ToLower(name) + ":" + strings.TrimSpace(value)
}

func relaxedBody(body []byte) []byte {
	text := normalizeCRLF(string(body))
	lines := strings.Split(text, "\r\n")
	for index, line := range lines {
		line = strings.TrimRight(line, " \t")
		var compact strings.Builder
		inWSP := false
		for _, char := range line {
			if char == ' ' || char == '\t' {
				if !inWSP {
					compact.WriteByte(' ')
					inWSP = true
				}
				continue
			}
			compact.WriteRune(char)
			inWSP = false
		}
		lines[index] = compact.String()
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return []byte("\r\n")
	}
	return []byte(strings.Join(lines, "\r\n") + "\r\n")
}
