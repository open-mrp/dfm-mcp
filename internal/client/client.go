// Package client is a small HTTP client for the DFM API, used by the MCP server. It speaks the API's own resource types, so what an agent sees is exactly what the API returns.
package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// VersionHeader names the API version a request speaks, and Version is the one this client speaks.
const (
	VersionHeader = "DFM-Version"
	Version       = "1.0.forge"
)

// Client calls one DFM API with one API key.
type Client struct {
	// BaseURL is the API's origin, such as https://dfm.openmrp.ai.
	BaseURL string
	// APIKey is sent as a bearer token; empty sends none.
	APIKey string
	// HTTP (optional; default: a client with a 60 s timeout) sends the requests.
	HTTP *http.Client
	// UserAgent (optional) identifies the caller.
	UserAgent string
}

// Error is an API error response, in the API's error shape, so an agent sees the same fields whether the service or the local engine refused.
type Error struct {
	Status      int
	Type        string          `json:"type"`
	Code        string          `json:"code"`
	Message     string          `json:"message"`
	Param       *string         `json:"param"`
	IsTransient bool            `json:"is_transient"`
	Errors      json.RawMessage `json:"errors"`
	DocURL      *string         `json:"doc_url"`
}

func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%d %s)", e.Message, e.Status, e.Code)
	if e.Param != nil {
		fmt.Fprintf(&b, " at %s", *e.Param)
	}
	if len(e.Errors) > 2 {
		fmt.Fprintf(&b, ": %s", e.Errors)
	}
	if e.IsTransient {
		b.WriteString(". This is temporary; the same request may succeed if retried")
	}
	if e.DocURL != nil && *e.DocURL != "" {
		fmt.Fprintf(&b, ". See %s", *e.DocURL)
	}
	return b.String()
}

// Permanent reports whether retrying the request cannot help: the service refused it (a 4xx other than a timeout or a rate limit).
func (e *Error) Permanent() bool {
	return e.Status >= 400 && e.Status < 500 && e.Status != http.StatusRequestTimeout && e.Status != http.StatusTooManyRequests
}

// Do sends one request and decodes the response into out (a pointer), or returns the API's error.
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	return c.DoIdempotent(ctx, method, path, query, body, out, "")
}

// maxAttempts bounds how often one call is sent: the first try and two retries of a transient failure.
const maxAttempts = 3

// DoIdempotent is Do with an Idempotency-Key, so a POST that is retried after a lost response does not run twice on the service. A POST sent without one gets a random key for this call, so the client can retry it safely: a dropped connection, a 429 or a 5xx is retried twice, after the service's Retry-After or a short backoff, with the same key, and the service answers a retry of a request it already ran with the first response.
func (c *Client) DoIdempotent(ctx context.Context, method, path string, query url.Values, body any, out any, idempotencyKey string) error {
	var data []byte
	if body != nil {
		var err error
		if data, err = json.Marshal(body); err != nil {
			return err
		}
	}
	if idempotencyKey == "" && method == http.MethodPost {
		idempotencyKey = randomKey()
	}
	for attempt := 1; ; attempt++ {
		wait, err := c.send(ctx, method, path, query, data, body != nil, out, idempotencyKey)
		if wait < 0 || attempt == maxAttempts {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(wait):
		}
	}
}

// randomKey is an Idempotency-Key for one call: 32 hex characters, longer than the 16 the service asks of a caller without an API key.
func randomKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// retryAfter says whether a failed attempt may be retried and after how long: a network error or a 429, 502, 503 or 504 may, after the service's Retry-After when it sent one; anything else may not (-1).
func retryAfter(resp *http.Response, err error, attempt time.Duration) time.Duration {
	if err != nil {
		return attempt
	}
	switch resp.StatusCode {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		if s, perr := strconv.Atoi(resp.Header.Get("Retry-After")); perr == nil && s >= 0 && s <= 30 {
			return time.Duration(s) * time.Second
		}
		return attempt
	}
	return -1
}

// send makes one attempt, returning how long to wait before another (negative when it should not be retried) and the outcome.
func (c *Client) send(ctx context.Context, method, path string, query url.Values, data []byte, hasBody bool, out any, idempotencyKey string) (time.Duration, error) {
	var reader io.Reader
	if hasBody {
		reader = bytes.NewReader(data)
	}
	u := strings.TrimRight(c.BaseURL, "/") + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return -1, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set(VersionHeader, Version)
	if hasBody {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}
	backoff := 500 * time.Millisecond
	resp, err := httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return -1, err
		}
		return retryAfter(nil, err, backoff), err
	}
	defer resp.Body.Close()
	respData, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return backoff, err
	}
	if resp.StatusCode >= 400 {
		wait := retryAfter(resp, nil, backoff)
		var envelope struct {
			Error *Error `json:"error"`
		}
		if json.Unmarshal(respData, &envelope) == nil && envelope.Error != nil {
			envelope.Error.Status = resp.StatusCode
			return wait, envelope.Error
		}
		return wait, &Error{Status: resp.StatusCode, Code: "unknown", Message: strings.TrimSpace(string(respData))}
	}
	if out == nil {
		return -1, nil
	}
	return -1, json.Unmarshal(respData, out)
}

// Upload PUTs a file to an upload target the service handed out. A target on the service's own origin (a deployment without a bucket) gets the key and version headers; a signed bucket URL gets exactly the headers it was signed with and nothing else.
func (c *Client) Upload(ctx context.Context, target string, headers map[string]string, data []byte) error {
	base := strings.TrimRight(c.BaseURL, "/")
	own := strings.HasPrefix(target, "/")
	if own {
		target = base + target
	} else if strings.HasPrefix(target, base+"/") {
		own = true
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, target, bytes.NewReader(data))
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if own {
		req.Header.Set(VersionHeader, Version)
		if c.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.APIKey)
		}
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Minute}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		var envelope struct {
			Error *Error `json:"error"`
		}
		if json.Unmarshal(body, &envelope) == nil && envelope.Error != nil {
			envelope.Error.Status = resp.StatusCode
			return envelope.Error
		}
		return &Error{Status: resp.StatusCode, Code: "upload_failed", Message: strings.TrimSpace(string(body))}
	}
	return nil
}
