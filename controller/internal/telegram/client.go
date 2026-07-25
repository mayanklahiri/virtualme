package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxResponseBytes = 1024 * 1024

type API interface {
	GetMe(context.Context) (User, error)
	GetUpdates(context.Context, GetUpdatesRequest) ([]Update, error)
	SendMessage(context.Context, SendMessageRequest) (Message, error)
	SendChatAction(context.Context, SendChatActionRequest) error
}

type APIError struct {
	Code       string
	Status     int
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("telegram: %s (status %d)", e.Code, e.Status)
	}
	return "telegram: " + e.Code
}

type Client struct {
	base  string
	token string
	http  *http.Client
}

func NewClient(base, token string, client *http.Client) (*Client, error) {
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || token == "" {
		return nil, errors.New("telegram: invalid client configuration")
	}
	if client == nil {
		client = &http.Client{}
	}
	return &Client{base: strings.TrimRight(base, "/"), token: token, http: client}, nil
}

func (c *Client) GetMe(ctx context.Context) (User, error) {
	var result User
	err := c.call(ctx, "getMe", struct{}{}, &result)
	return result, err
}

func (c *Client) GetUpdates(ctx context.Context, request GetUpdatesRequest) ([]Update, error) {
	var result []Update
	err := c.call(ctx, "getUpdates", request, &result)
	return result, err
}

func (c *Client) SendMessage(ctx context.Context, request SendMessageRequest) (Message, error) {
	var result Message
	child, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	err := c.call(child, "sendMessage", request, &result)
	return result, err
}

func (c *Client) SendChatAction(ctx context.Context, request SendChatActionRequest) error {
	child, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var result bool
	return c.call(child, "sendChatAction", request, &result)
}

func (c *Client) call(ctx context.Context, method string, input, output any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return &APIError{Code: "protocol_error"}
	}
	endpoint := c.base + "/bot" + url.PathEscape(c.token) + "/" + method
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return &APIError{Code: "transport_error"}
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &APIError{Code: "transport_error"}
	}
	defer response.Body.Close()
	reader := io.LimitReader(response.Body, maxResponseBytes+1)
	raw, err := io.ReadAll(reader)
	if err != nil {
		return &APIError{Code: "invalid_response", Status: response.StatusCode}
	}
	if len(raw) > maxResponseBytes {
		return &APIError{Code: "response_too_large", Status: response.StatusCode}
	}
	envelope := struct {
		OK         bool               `json:"ok"`
		Result     json.RawMessage    `json:"result"`
		ErrorCode  int                `json:"error_code"`
		Parameters ResponseParameters `json:"parameters"`
	}{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if decoder.Decode(&envelope) != nil {
		return &APIError{Code: "invalid_response", Status: response.StatusCode}
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return &APIError{Code: "invalid_response", Status: response.StatusCode}
	}
	status := envelope.ErrorCode
	if status == 0 && (response.StatusCode < 200 || response.StatusCode >= 300) {
		status = response.StatusCode
	}
	if !envelope.OK || response.StatusCode < 200 || response.StatusCode >= 300 {
		return classifyAPIError(status, envelope.Parameters.RetryAfter)
	}
	if len(envelope.Result) == 0 || json.Unmarshal(envelope.Result, output) != nil {
		return &APIError{Code: "invalid_response", Status: response.StatusCode}
	}
	return nil
}

func classifyAPIError(status, retry int) error {
	code := "remote_unavailable"
	switch {
	case status == 401:
		code = "authentication_failed"
	case status == 409:
		code = "poll_conflict"
	case status == 429:
		code = "rate_limited"
	case status == 400:
		code = "protocol_error"
	case status >= 400 && status < 500:
		code = "api_rejected"
	}
	return &APIError{Code: code, Status: status, RetryAfter: time.Duration(retry) * time.Second}
}
