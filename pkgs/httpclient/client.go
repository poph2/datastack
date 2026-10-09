// Package httpclient is the JSON REST client services use to call each other.
// It propagates the trace (so a checkout is one trace across every service),
// enforces a timeout per peer, records client-side metrics, and turns error
// responses into *APIError carrying the peer's stable error code.
package httpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// maxErrorBody bounds how much of an error response is kept on APIError.
const maxErrorBody = 64 << 10

// Client calls one peer service.
type Client struct {
	peer    string
	baseURL string
	http    *http.Client
}

// New returns a client for the peer service at baseURL (for example
// "http://payment-service:8084"). peer names it in spans and metrics; every
// call is bounded by timeout.
func New(peer, baseURL string, timeout time.Duration) *Client {
	transport := otelhttp.NewTransport(http.DefaultTransport,
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return r.Method + " " + peer
		}),
	)
	return &Client{
		peer:    peer,
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Transport: transport, Timeout: timeout},
	}
}

// APIError is a non-2xx response. Code is the peer's machine-readable error
// code (for example OUT_OF_STOCK), and is empty if the body wasn't the shared
// error shape. Body keeps the raw response for callers that need more fields.
type APIError struct {
	Peer       string
	StatusCode int
	Code       string
	Message    string
	Body       []byte
}

func (e *APIError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("%s responded %d", e.Peer, e.StatusCode)
	}
	return fmt.Sprintf("%s responded %d %s: %s", e.Peer, e.StatusCode, e.Code, e.Message)
}

// ErrorCode returns the peer's error code if err is (or wraps) an *APIError,
// and "" otherwise, including for transport errors and timeouts.
func ErrorCode(err error) string {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	return ""
}

// Do sends body as JSON to path and decodes a 2xx response into out. Either
// may be nil. The request carries ctx, so it joins the caller's trace and is
// cancelled with it.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	req, err := c.newRequest(ctx, method, path, body)
	if err != nil {
		return err
	}

	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		observe(c.peer, method, "error", start)
		return fmt.Errorf("calling %s %s %s: %w", c.peer, method, path, err)
	}
	defer resp.Body.Close()
	observe(c.peer, method, strconv.Itoa(resp.StatusCode), start)

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return c.apiError(resp)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding %s %s %s response: %w", c.peer, method, path, err)
	}
	return nil
}

func (c *Client) newRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encoding %s %s request: %w", c.peer, path, err)
		}
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, fmt.Errorf("building %s %s request: %w", c.peer, path, err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (c *Client) apiError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	apiErr := &APIError{Peer: c.peer, StatusCode: resp.StatusCode, Body: body}

	var shaped struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &shaped) == nil {
		apiErr.Code, apiErr.Message = shaped.Error, shaped.Message
	}
	return apiErr
}
