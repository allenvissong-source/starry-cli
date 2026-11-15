package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Config struct {
	BackendURL   string
	HTTPClient   *http.Client
	PublicWebURL string
}
type Client struct {
	base      string
	client    *http.Client
	publicWeb string
}

func (b *Client) BackendURL() string       { return b.base }
func (b *Client) HTTPClient() *http.Client { return b.client }
func (b *Client) PublicWebURL() string     { return b.publicWeb }

type Response struct {
	Data   json.RawMessage
	Status int
	Header http.Header
}
type APIError struct {
	Status            int    `json:"-"`
	Code              string `json:"code"`
	Message           string `json:"message"`
	Retryable         bool   `json:"retryable"`
	RetryAfterSeconds *int   `json:"retryAfterSeconds"`
}

func (e *APIError) Error() string { return e.Code }

func NewAPIError(status int, code string) *APIError {
	return &APIError{Status: status, Code: code, Message: code, Retryable: retryableStatus(status)}
}

func retryableStatus(status int) bool {
	return status == 429 || status >= 500
}

func NewClient(cfg Config) (*Client, error) {
	u, err := url.Parse(cfg.BackendURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Path != "" && u.Path != "/") || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"))) {
		return nil, errors.New("backend URL must be HTTPS or loopback HTTP")
	}
	if cfg.PublicWebURL != "" {
		web, err := url.Parse(cfg.PublicWebURL)
		if err != nil || web.Host == "" || web.User != nil || web.RawQuery != "" || web.Fragment != "" ||
			(web.Path != "" && web.Path != "/") || (web.Scheme != "https" && !(web.Scheme == "http" && (web.Hostname() == "127.0.0.1" || web.Hostname() == "localhost" || web.Hostname() == "::1"))) {
			return nil, errors.New("public web URL must be HTTPS or loopback HTTP origin")
		}
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{base: strings.TrimRight(cfg.BackendURL, "/"), client: &copyClient, publicWeb: strings.TrimRight(cfg.PublicWebURL, "/")}, nil
}
func (b *Client) Request(ctx context.Context, method, path, token string, body any) (json.RawMessage, error) {
	return b.RequestWithKey(ctx, method, path, token, "", body)
}

func (b *Client) RequestWithKey(ctx context.Context, method, path, token, requestKey string, body any) (json.RawMessage, error) {
	result, err := b.Do(ctx, method, path, token, requestKey, body)
	return result.Data, err
}

// Do preserves status and headers while enforcing the common JSON boundary.
func (b *Client) Do(ctx context.Context, method, path, token, requestKey string, body any) (Response, error) {
	parsed, parseErr := url.Parse(path)
	if !strings.HasPrefix(path, "/api/v1/") && path != "/internal/v1/integration-token/exchange" {
		return Response{}, errors.New("invalid API path")
	}
	if parseErr != nil || parsed.IsAbs() || parsed.Host != "" || parsed.Fragment != "" ||
		strings.Contains(parsed.Path, "..") || strings.ContainsAny(path, "\r\n\\") {
		return Response{}, errors.New("invalid API path")
	}
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return Response{}, err
		}
		if len(data) > 64<<10 {
			return Response{}, NewAPIError(400, "request_too_large")
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, b.base+path, reader)
	if err != nil {
		return Response{}, err
	}
	// net/http otherwise replays bodies carrying Idempotency-Key on a broken
	// reused connection. An uncertain write must be queried by its caller.
	if method != http.MethodGet && method != http.MethodHead {
		req.GetBody = nil
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if requestKey != "" {
		req.Header.Set("Idempotency-Key", requestKey)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return b.send(req)
}

// UploadRaw streams bounded CAS bytes to the configured backend only.
func (b *Client) UploadRaw(ctx context.Context, path, token, mediaType string, size int64, body io.Reader) (Response, error) {
	if !strings.HasPrefix(path, "/api/v1/authoring/cas/blobs/") ||
		len(strings.TrimPrefix(path, "/api/v1/authoring/cas/blobs/")) != 64 ||
		strings.Trim(strings.TrimPrefix(path, "/api/v1/authoring/cas/blobs/"), "0123456789abcdef") != "" ||
		size < 1 || size > 128<<20 || strings.ContainsAny(mediaType, "\r\n") {
		return Response{}, NewAPIError(400, "invalid_upload")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, b.base+path, io.LimitReader(body, size))
	if err != nil {
		return Response{}, err
	}
	req.GetBody = nil
	req.ContentLength = size
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", mediaType)
	req.Header.Set("Accept", "application/json")
	return b.send(req)
}

func (b *Client) send(req *http.Request) (Response, error) {
	resp, err := b.client.Do(req)
	if err != nil {
		if contextErr := req.Context().Err(); contextErr != nil {
			return Response{}, contextErr
		}
		return Response{}, NewAPIError(503, "upstream_unavailable")
	}
	defer resp.Body.Close()
	result := Response{Status: resp.StatusCode, Header: resp.Header.Clone()}
	if resp.StatusCode == http.StatusNoContent && req.Method != http.MethodGet {
		return result, nil
	}
	var envelope struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
		Error   *struct {
			Code              string `json:"code"`
			Message           string `json:"message"`
			Retryable         *bool  `json:"retryable"`
			RetryAfterSeconds *int   `json:"retryAfterSeconds"`
		} `json:"error"`
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20+1))
	if err != nil {
		if contextErr := req.Context().Err(); contextErr != nil {
			return Response{}, contextErr
		}
	}
	if len(data) > 4<<20 {
		return Response{}, NewAPIError(resp.StatusCode, "response_too_large")
	}
	if err != nil || json.Unmarshal(data, &envelope) != nil {
		return Response{}, NewAPIError(503, "upstream_unavailable")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !envelope.Success {
		code := "upstream_unavailable"
		message := code
		retryable := retryableStatus(resp.StatusCode)
		var retryAfter *int
		if envelope.Error != nil {
			if envelope.Error.Code != "" {
				code = envelope.Error.Code
			}
			if envelope.Error.Message != "" {
				message = envelope.Error.Message
			}
			if envelope.Error.Retryable != nil {
				retryable = *envelope.Error.Retryable
			}
			retryAfter = envelope.Error.RetryAfterSeconds
		}
		return result, &APIError{Status: resp.StatusCode, Code: code, Message: message, Retryable: retryable, RetryAfterSeconds: retryAfter}
	}
	result.Data = envelope.Data
	return result, nil
}
