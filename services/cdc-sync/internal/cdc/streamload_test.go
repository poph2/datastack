package cdc

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestLoader(url string) *StreamLoader {
	return NewStreamLoader(StreamLoadConfig{URL: url, Database: "analytics_db", User: "root", Password: "secret"})
}

// respondWith serves a fixed Stream Load response body with HTTP 200, as
// StarRocks does even for failed loads.
func respondWith(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, body)
	}
}

func TestStreamLoadOutcomes(t *testing.T) {
	cases := map[string]struct {
		body    string
		want    LoadOutcome
		wantErr string
	}{
		"success":         {body: `{"Status": "Success", "Message": "OK"}`, want: Loaded},
		"publish timeout": {body: `{"Status": "Publish Timeout"}`, want: Loaded},
		"label exists, finished": {
			body: `{"Status": "Label Already Exists", "ExistingJobStatus": "FINISHED"}`, want: AlreadyLoaded,
		},
		"label exists, still running": {
			body: `{"Status": "Label Already Exists", "ExistingJobStatus": "RUNNING"}`, wantErr: "RUNNING",
		},
		"HTTP 200 with failed status": {
			body: `{"Status": "Fail", "Message": "too many filtered rows", "ErrorURL": "http://be/err"}`, wantErr: "too many filtered rows",
		},
		"not JSON": {body: `<html>bad gateway</html>`, wantErr: "unreadable body"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(respondWith(tc.body))
			defer server.Close()

			outcome, err := newTestLoader(server.URL).Load(context.Background(), "orders", "label_1", []byte(`[]`))

			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, outcome)
		})
	}
}

func TestStreamLoadRequest(t *testing.T) {
	var got *http.Request
	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got, gotBody = r, string(body)
		_, _ = io.WriteString(w, `{"Status": "Success"}`)
	}))
	defer server.Close()

	_, err := newTestLoader(server.URL+"/").Load(context.Background(), "orders", "label_1", []byte(`[{"id":1}]`))
	require.NoError(t, err)

	assert.Equal(t, http.MethodPut, got.Method)
	assert.Equal(t, "/api/analytics_db/orders/_stream_load", got.URL.Path)
	assert.Equal(t, "json", got.Header.Get("format"))
	assert.Equal(t, "true", got.Header.Get("strip_outer_array"))
	assert.Equal(t, "label_1", got.Header.Get("label"))
	user, password, ok := got.BasicAuth()
	assert.True(t, ok)
	assert.Equal(t, "root", user)
	assert.Equal(t, "secret", password)
	assert.Equal(t, `[{"id":1}]`, gotBody)
}

func TestStreamLoadFollowsRedirectWithAuth(t *testing.T) {
	var gotAuth, gotBody string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotAuth, gotBody = r.Header.Get("Authorization"), string(body)
		_, _ = io.WriteString(w, `{"Status": "Success"}`)
	}))
	defer backend.Close()

	// The redirect names the backend as "localhost" while the frontend is
	// reached as 127.0.0.1. The host changes, which is when net/http drops
	// the Authorization header unless CheckRedirect puts it back.
	backendURL := strings.Replace(backend.URL, "127.0.0.1", "localhost", 1)
	frontend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, backendURL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer frontend.Close()

	outcome, err := newTestLoader(frontend.URL).Load(context.Background(), "orders", "label_1", []byte(`[{"id":1}]`))

	require.NoError(t, err)
	assert.Equal(t, Loaded, outcome)
	assert.NotEmpty(t, gotAuth, "the redirected load must still be authenticated")
	assert.Equal(t, `[{"id":1}]`, gotBody, "the body is replayed on the redirect")
}

func TestLoadLabel(t *testing.T) {
	from := Cursor{UpdatedAt: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC), ID: 0}
	to := Cursor{UpdatedAt: time.Date(2026, 10, 8, 12, 0, 1, 500, time.UTC), ID: 42}

	label := loadLabel(TableRef{Database: "orders", Table: "orders"}, from, to)

	assert.Equal(t, "cdc_orders_orders_1791460800000000_0_1791460801000000_42", label)
	assert.Equal(t, label, loadLabel(TableRef{Database: "orders", Table: "orders"}, from, to), "deterministic")
	assert.Regexp(t, `^[-_A-Za-z0-9]+$`, label)
}

func TestLoadLabelStaysWithinLimit(t *testing.T) {
	long := TableRef{Database: strings.Repeat("d", 100), Table: strings.Repeat("t", 100)}
	to := Cursor{UpdatedAt: time.Now(), ID: 1 << 62}

	label := loadLabel(long, Cursor{UpdatedAt: epoch}, to)

	assert.LessOrEqual(t, len(label), maxLabelLength)
	assert.Contains(t, label, "_4611686018427387904", "the cursor part survives truncation")
}

func TestLoadLabelSanitises(t *testing.T) {
	label := loadLabel(TableRef{Database: "my-db", Table: "t.able"}, Cursor{UpdatedAt: epoch}, Cursor{UpdatedAt: epoch, ID: 1})
	assert.Regexp(t, `^[-_A-Za-z0-9]+$`, label)
}
