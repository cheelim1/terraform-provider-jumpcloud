package jumpcloud

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A server that accepts the connection but never answers must not hang the raw
// application helpers (and with them refresh/apply, while Terraform holds its
// state lock).
func TestApplicationRawRequestTimesOutOnStalledServer(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)

	orig := applicationRequestTimeout
	applicationRequestTimeout = 100 * time.Millisecond
	defer func() { applicationRequestTimeout = orig }()

	start := time.Now()
	if _, err := ApplicationGetRaw(server.URL, map[string]string{"x-api-key": "test-key"}, "app1"); err == nil {
		t.Fatal("a stalled GET must fail rather than hang")
	}
	if _, err := ApplicationCreateRaw(server.URL, map[string]string{"x-api-key": "test-key"}, map[string]interface{}{"name": "x"}); err == nil {
		t.Fatal("a stalled write must fail rather than hang")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("took %s; expected the 100ms timeout to bound each request", elapsed)
	}
}
