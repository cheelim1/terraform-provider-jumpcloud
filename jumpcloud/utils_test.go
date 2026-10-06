package jumpcloud

import (
	"errors"
	"net/http"
	"testing"
)

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
