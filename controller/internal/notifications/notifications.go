// Package notifications implements durable server-wide operator notifications.
package notifications

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxDetailBytes = 8192
	ulidAlphabet   = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
)

var (
	senderPattern    = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,31}$`)
	subtypePattern   = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,47}$`)
	requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)
	forbiddenKeys    = map[string]bool{
		"html": true, "svg": true, "innerhtml": true, "script": true,
		"style": true, "renderer": true, "component": true,
	}
)

// Detail is a versioned, renderer-selected structured notification payload.
type Detail struct {
	Version  int             `json:"version"`
	Renderer string          `json:"renderer"`
	Data     json.RawMessage `json:"data"`
}

// Notification is one immutable notification with mutable global read state.
type Notification struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	Subtype      string `json:"subtype,omitempty"`
	Sender       string `json:"sender"`
	Title        string `json:"title"`
	Summary      string `json:"summary"`
	OccurredAtMS int64  `json:"occurredAtMs"`
	CreatedAtMS  int64  `json:"createdAtMs"`
	ReadAtMS     int64  `json:"readAtMs,omitempty"`
	Detail       Detail `json:"detail"`
}

// CreateRequest is trusted producer input. IDs are reserved for recovery.
type CreateRequest struct {
	ID           string
	Type         string
	Subtype      string
	Sender       string
	Title        string
	Summary      string
	OccurredAtMS int64
	Renderer     string
	Detail       json.RawMessage
}

// Creator is the narrow producer seam consumed by agent/config/Telegram code.
type Creator interface {
	Create(context.Context, CreateRequest) (Notification, error)
}

// TypeDefinition describes one server-authoritative presentation type.
type TypeDefinition struct {
	Name             string   `json:"name"`
	Icon             string   `json:"icon"`
	DefaultRenderer  string   `json:"defaultRenderer"`
	AllowedRenderers []string `json:"-"`
}

var typeRegistry = []TypeDefinition{
	{Name: "info", Icon: "i-circle-info", DefaultRenderer: "generic", AllowedRenderers: []string{"generic", "lifecycle", "configuration", "agent"}},
	{Name: "success", Icon: "i-circle-check", DefaultRenderer: "generic", AllowedRenderers: []string{"generic", "lifecycle", "configuration", "agent"}},
	{Name: "warning", Icon: "i-triangle-alert", DefaultRenderer: "generic", AllowedRenderers: []string{"generic", "lifecycle", "agent"}},
	{Name: "error", Icon: "i-circle-x", DefaultRenderer: "generic", AllowedRenderers: []string{"generic", "lifecycle", "agent"}},
}

// Registry returns a deep defensive copy in stable display order.
func Registry() []TypeDefinition {
	result := make([]TypeDefinition, len(typeRegistry))
	for i, definition := range typeRegistry {
		result[i] = definition
		result[i].AllowedRenderers = append([]string(nil), definition.AllowedRenderers...)
	}
	return result
}

func validateCreate(request CreateRequest, now time.Time) (Notification, error) {
	if request.ID != "" && !validULID(request.ID) {
		return Notification{}, errors.New("invalid notification ID")
	}
	var definition *TypeDefinition
	for i := range typeRegistry {
		if typeRegistry[i].Name == request.Type {
			definition = &typeRegistry[i]
			break
		}
	}
	if definition == nil {
		return Notification{}, fmt.Errorf("unknown notification type %q", request.Type)
	}
	if !senderPattern.MatchString(request.Sender) {
		return Notification{}, errors.New("invalid notification sender")
	}
	if request.Subtype != "" && !subtypePattern.MatchString(request.Subtype) {
		return Notification{}, errors.New("invalid notification subtype")
	}
	renderer := request.Renderer
	if renderer == "" {
		renderer = definition.DefaultRenderer
	}
	allowed := false
	for _, candidate := range definition.AllowedRenderers {
		allowed = allowed || candidate == renderer
	}
	if !allowed {
		return Notification{}, fmt.Errorf("renderer %q is not allowed for type %q", renderer, request.Type)
	}
	title := sanitizeText(request.Title)
	if count := utf8.RuneCountInString(title); count == 0 {
		return Notification{}, errors.New("title is required")
	} else if count > 120 {
		return Notification{}, errors.New("title exceeds 120 code points")
	}
	summary := sanitizeText(request.Summary)
	if count := utf8.RuneCountInString(summary); count == 0 {
		return Notification{}, errors.New("summary is required")
	} else if count > 240 {
		return Notification{}, errors.New("summary exceeds 240 code points")
	}
	detail, err := sanitizeDetail(request.Detail)
	if err != nil {
		return Notification{}, err
	}
	created := now.UnixMilli()
	occurred := request.OccurredAtMS
	if occurred == 0 {
		occurred = created
	}
	if occurred > created+5*60*1000 {
		return Notification{}, errors.New("occurredAtMs is too far in the future")
	}
	return Notification{
		ID: request.ID, Type: request.Type, Subtype: request.Subtype, Sender: request.Sender,
		Title: title, Summary: summary, OccurredAtMS: occurred, CreatedAtMS: created,
		Detail: Detail{Version: 1, Renderer: renderer, Data: detail},
	}, nil
}

func sanitizeText(input string) string {
	input = strings.ToValidUTF8(input, "�")
	var output strings.Builder
	space := true
	for _, r := range input {
		if unicode.IsSpace(r) {
			if !space {
				output.WriteByte(' ')
				space = true
			}
			continue
		}
		if isRemovedRune(r) {
			continue
		}
		output.WriteRune(r)
		space = false
	}
	return strings.TrimSpace(output.String())
}

func isRemovedRune(r rune) bool {
	return r <= 0x1f || (r >= 0x7f && r <= 0x9f) ||
		(r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) || r == 0xfeff
}

type detailLimits struct {
	depth int
	nodes int
}

func sanitizeDetail(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if len(raw) > maxDetailBytes {
		return nil, errors.New("detail exceeds 8192 bytes")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	limits := new(detailLimits)
	value, err := decodeSanitized(decoder, limits, 1)
	if err != nil {
		return nil, err
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, errors.New("detail must be a JSON object")
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err == nil {
			_ = token
		}
		return nil, errors.New("detail must contain one JSON object")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, errors.New("invalid notification detail")
	}
	if len(encoded) > maxDetailBytes {
		return nil, errors.New("detail exceeds 8192 bytes")
	}
	return encoded, nil
}

func decodeSanitized(decoder *json.Decoder, limits *detailLimits, depth int) (any, error) {
	if depth > 8 {
		return nil, errors.New("detail exceeds nesting depth 8")
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, errors.New("invalid notification detail")
	}
	switch value := token.(type) {
	case json.Delim:
		switch value {
		case '{':
			result := map[string]any{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, errors.New("invalid notification detail")
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, errors.New("invalid notification detail key")
				}
				key = sanitizeText(key)
				if count := utf8.RuneCountInString(key); count == 0 || count > 64 {
					return nil, errors.New("detail key must contain 1-64 code points")
				}
				if forbiddenKeys[strings.ToLower(key)] {
					return nil, fmt.Errorf("detail key %q is forbidden", key)
				}
				if _, duplicate := result[key]; duplicate {
					return nil, errors.New("duplicate detail key after sanitization")
				}
				limits.nodes++
				if limits.nodes > 256 {
					return nil, errors.New("detail exceeds 256 elements")
				}
				item, err := decodeSanitized(decoder, limits, depth+1)
				if err != nil {
					return nil, err
				}
				result[key] = item
			}
			if _, err := decoder.Token(); err != nil {
				return nil, errors.New("invalid notification detail")
			}
			return result, nil
		case '[':
			result := []any{}
			for decoder.More() {
				limits.nodes++
				if limits.nodes > 256 {
					return nil, errors.New("detail exceeds 256 elements")
				}
				item, err := decodeSanitized(decoder, limits, depth+1)
				if err != nil {
					return nil, err
				}
				result = append(result, item)
			}
			if _, err := decoder.Token(); err != nil {
				return nil, errors.New("invalid notification detail")
			}
			return result, nil
		}
	case string:
		value = sanitizeText(value)
		if utf8.RuneCountInString(value) > 2048 {
			return nil, errors.New("detail string exceeds 2048 code points")
		}
		return value, nil
	case json.Number, bool:
		return value, nil
	case nil:
		return nil, nil
	}
	return nil, errors.New("invalid notification detail")
}

func validRequestID(value string) bool { return requestIDPattern.MatchString(value) }

func validULID(value string) bool {
	if len(value) != 26 || value[0] > '7' {
		return false
	}
	for _, char := range value {
		if !strings.ContainsRune(ulidAlphabet, char) {
			return false
		}
	}
	return true
}

type ulidGenerator struct {
	mu      sync.Mutex
	clock   func() time.Time
	entropy io.Reader
	lastMS  uint64
	random  [10]byte
	seeded  bool
}

func newULIDGenerator(clock func() time.Time, entropy io.Reader) *ulidGenerator {
	return &ulidGenerator{clock: clock, entropy: entropy}
}

func (g *ulidGenerator) next() (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for {
		now := uint64(g.clock().UnixMilli())
		if !g.seeded || now > g.lastMS {
			if _, err := io.ReadFull(g.entropy, g.random[:]); err != nil {
				return "", fmt.Errorf("notification entropy: %w", err)
			}
			g.lastMS, g.seeded = now, true
		} else if incrementEntropy(&g.random) {
			for now <= g.lastMS {
				time.Sleep(time.Millisecond)
				now = uint64(g.clock().UnixMilli())
			}
			if _, err := io.ReadFull(g.entropy, g.random[:]); err != nil {
				return "", fmt.Errorf("notification entropy: %w", err)
			}
			g.lastMS = now
		}
		return encodeULID(g.lastMS, g.random), nil
	}
}

func incrementEntropy(value *[10]byte) (overflow bool) {
	for i := len(value) - 1; i >= 0; i-- {
		value[i]++
		if value[i] != 0 {
			return false
		}
	}
	return true
}

func encodeULID(ms uint64, entropy [10]byte) string {
	var data [16]byte
	data[0], data[1], data[2] = byte(ms>>40), byte(ms>>32), byte(ms>>24)
	data[3], data[4], data[5] = byte(ms>>16), byte(ms>>8), byte(ms)
	copy(data[6:], entropy[:])
	var encoded [26]byte
	for character := range encoded {
		var value byte
		for bit := range 5 {
			sourceBit := character*5 + bit - 2
			value <<= 1
			if sourceBit >= 0 {
				value |= (data[sourceBit/8] >> (7 - sourceBit%8)) & 1
			}
		}
		encoded[character] = ulidAlphabet[value]
	}
	return string(encoded[:])
}

func defaultULIDGenerator() *ulidGenerator {
	return newULIDGenerator(time.Now, rand.Reader)
}
