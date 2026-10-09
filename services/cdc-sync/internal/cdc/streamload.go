package cdc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	// loadTimeout bounds one Stream Load request. Batches are at most a few
	// MB, so anything slower means StarRocks is in trouble.
	loadTimeout = 60 * time.Second
	// maxLabelLength is StarRocks' limit on a load label.
	maxLabelLength = 128
	// maxRedirects matches net/http's default.
	maxRedirects = 10
	// maxResponseBytes caps how much of a load response is read.
	maxResponseBytes = 1 << 20
)

// Stream Load statuses (the "Status" field of the response body).
const (
	loadStatusSuccess        = "Success"
	loadStatusPublishTimeout = "Publish Timeout"
	loadStatusLabelExists    = "Label Already Exists"
)

// Job states that mean the earlier load holding a label has committed. A
// label held by a job that is still running might yet fail.
var committedJobStates = map[string]bool{"": true, "FINISHED": true, "COMMITTED": true, "VISIBLE": true}

var labelUnsafeChars = regexp.MustCompile(`[^-_A-Za-z0-9]`)

// LoadOutcome says how a successful load landed.
type LoadOutcome int

const (
	// Loaded means StarRocks committed this request's rows.
	Loaded LoadOutcome = iota
	// AlreadyLoaded means an earlier request with the same label committed
	// them: a retried batch that had landed before.
	AlreadyLoaded
)

// StreamLoadConfig locates StarRocks' Stream Load endpoint.
type StreamLoadConfig struct {
	// URL is the BE HTTP address (for example http://starrocks:8040). Loads
	// go straight to the BE: the FE would redirect to a BE address it
	// advertises, which in the all-in-one image is often 127.0.0.1 and
	// unreachable from another pod.
	URL      string
	Database string
	User     string
	Password string
}

// StreamLoader sends batches to StarRocks with HTTP Stream Load.
type StreamLoader struct {
	config StreamLoadConfig
	client *http.Client
}

// NewStreamLoader returns a StreamLoader for config.
func NewStreamLoader(config StreamLoadConfig) *StreamLoader {
	loader := &StreamLoader{config: config}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	loader.client = &http.Client{
		Transport:     transport,
		Timeout:       loadTimeout,
		CheckRedirect: loader.keepAuthOnRedirect,
	}
	return loader
}

// keepAuthOnRedirect re-applies basic auth on a redirect. net/http drops the
// Authorization header when a redirect changes host, which is exactly what a
// FE-to-BE redirect does, so the redirected load would fail authentication.
// The bytes.Reader body lets net/http replay it on the 307.
func (l *StreamLoader) keepAuthOnRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}
	req.SetBasicAuth(l.config.User, l.config.Password)
	return nil
}

// loadResponse is the part of the Stream Load response body that matters.
type loadResponse struct {
	Status            string `json:"Status"`
	Message           string `json:"Message"`
	ExistingJobStatus string `json:"ExistingJobStatus"`
	ErrorURL          string `json:"ErrorURL"`
}

// Load upserts body (a JSON array of row objects) into table under label.
//
// StarRocks reports the outcome in the response body, and can answer HTTP 200
// for a failed load, so the body decides. Success and Publish Timeout (the
// data is committed, just not yet visible) count as loaded. Label Already
// Exists for a committed job is a retried batch that had already landed.
func (l *StreamLoader) Load(ctx context.Context, table, label string, body []byte) (LoadOutcome, error) {
	req, err := l.newRequest(ctx, table, label, body)
	if err != nil {
		return 0, err
	}

	resp, err := l.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("stream load into %s: %w", table, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return 0, fmt.Errorf("reading stream load response for %s: %w", table, err)
	}

	var result loadResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return 0, fmt.Errorf("stream load into %s: HTTP %d, unreadable body %q", table, resp.StatusCode, truncate(string(raw)))
	}
	return interpretLoadResponse(table, result)
}

func (l *StreamLoader) newRequest(ctx context.Context, table, label string, body []byte) (*http.Request, error) {
	url := fmt.Sprintf("%s/api/%s/%s/_stream_load", strings.TrimRight(l.config.URL, "/"), l.config.Database, table)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building stream load request: %w", err)
	}

	req.SetBasicAuth(l.config.User, l.config.Password)
	req.Header.Set("Expect", "100-continue")
	req.Header.Set("format", "json")
	req.Header.Set("strip_outer_array", "true")
	req.Header.Set("label", label)
	return req, nil
}

func interpretLoadResponse(table string, result loadResponse) (LoadOutcome, error) {
	switch {
	case result.Status == loadStatusSuccess || result.Status == loadStatusPublishTimeout:
		return Loaded, nil
	case result.Status == loadStatusLabelExists && committedJobStates[result.ExistingJobStatus]:
		return AlreadyLoaded, nil
	case result.Status == loadStatusLabelExists:
		return 0, fmt.Errorf("stream load into %s: label held by a %s job", table, result.ExistingJobStatus)
	default:
		return 0, fmt.Errorf("stream load into %s: status %q: %s (errors: %s)",
			table, result.Status, result.Message, result.ErrorURL)
	}
}

// loadLabel names a batch deterministically from where it starts and ends,
// so a retry of the same batch reuses the label and StarRocks refuses to load
// it twice. The table prefix is shortened if the label would be too long.
func loadLabel(ref TableRef, from, to Cursor) string {
	suffix := fmt.Sprintf("_%d_%d_%d_%d", from.UpdatedAt.UnixMicro(), from.ID, to.UpdatedAt.UnixMicro(), to.ID)
	prefix := "cdc_" + ref.Database + "_" + ref.Table

	if room := maxLabelLength - len(suffix); len(prefix) > room {
		prefix = prefix[:max(room, 0)]
	}
	label := labelUnsafeChars.ReplaceAllString(prefix+suffix, "_")
	return label[:min(len(label), maxLabelLength)]
}

// truncate shortens text for error messages.
func truncate(text string) string {
	const limit = 200
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "..."
}
