package jumpcloud

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Raw application helpers must send the provider's configured x-org-id, not
// just the API key, and must not send headers that are unset.
func TestApplicationGetRawSendsConfiguredHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != "test-key" {
			t.Errorf("x-api-key = %q, want %q", got, "test-key")
		}
		if got := r.Header.Get("x-org-id"); got != "org-123" {
			t.Errorf("x-org-id = %q, want %q", got, "org-123")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"_id":"app1"}`))
	}))
	defer server.Close()

	headers := map[string]string{"x-api-key": "test-key", "x-org-id": "org-123"}
	result, err := ApplicationGetRaw(server.URL, headers, "app1")
	if err != nil {
		t.Fatalf("ApplicationGetRaw: %v", err)
	}
	if result["_id"] != "app1" {
		t.Errorf("_id = %v, want app1", result["_id"])
	}
}

func TestJcRawRequestOmitsUnsetHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, present := r.Header["X-Org-Id"]; present {
			t.Errorf("x-org-id must not be sent when unset")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"_id":"app1"}`))
	}))
	defer server.Close()

	if _, err := ApplicationGetRaw(server.URL, map[string]string{"x-api-key": "test-key", "x-org-id": ""}, "app1"); err != nil {
		t.Fatalf("ApplicationGetRaw: %v", err)
	}
}

func TestIsNotFound(t *testing.T) {
	cases := []struct {
		name string
		res  *http.Response
		err  error
		want bool
	}{
		{"no error", &http.Response{StatusCode: http.StatusNotFound}, nil, false},
		{"404 response", &http.Response{StatusCode: http.StatusNotFound}, errors.New("Status: 404 Not Found"), true},
		{"EOF with no response", nil, errors.New("EOF"), true},
		{"500 response", &http.Response{StatusCode: http.StatusInternalServerError}, errors.New("Status: 500"), false},
		{"network error", nil, errors.New("connection refused"), false},
	}
	for _, c := range cases {
		if got := isNotFound(c.res, c.err); got != c.want {
			t.Errorf("%s: isNotFound() = %v, want %v", c.name, got, c.want)
		}
	}
}
