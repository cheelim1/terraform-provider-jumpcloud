package jumpcloud

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

var testHeaders = map[string]string{"x-api-key": "test-key"}

// Raw helpers must carry the provider's configured x-org-id, not just the API
// key, or multi-tenant setups would hit the key's default organization.
func TestJcRawHelpersSendOrgHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "test-key", r.Header.Get("x-api-key"))
		assert.Equal(t, "org-123", r.Header.Get("x-org-id"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "abc"})
	}))
	defer server.Close()

	headers := map[string]string{"x-api-key": "test-key", "x-org-id": "org-123"}
	_, err := jcGetRaw(server.URL, headers, "/widgets/abc")
	require.NoError(t, err)
	_, err = jcWriteRaw(http.MethodPut, server.URL, headers, "/widgets/abc", map[string]interface{}{"enabled": true})
	require.NoError(t, err)
}

func TestJcRawHelpersOmitUnsetOrgHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, present := r.Header["X-Org-Id"]
		assert.False(t, present)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "abc"})
	}))
	defer server.Close()

	_, err := jcGetRaw(server.URL, map[string]string{"x-api-key": "test-key", "x-org-id": ""}, "/widgets/abc")
	require.NoError(t, err)
}

// A server that accepts the connection but never answers must not hang the
// raw helpers (and with them refresh/apply, while Terraform holds its lock).
func TestJcRawHelpersTimeOutOnStalledServer(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)

	orig := rawRequestTimeout
	rawRequestTimeout = 100 * time.Millisecond
	defer func() { rawRequestTimeout = orig }()

	start := time.Now()
	_, err := jcGetRaw(server.URL, testHeaders, "/widgets/abc")
	require.Error(t, err, "a stalled GET must fail rather than hang")
	_, err = jcWriteRaw(http.MethodPut, server.URL, testHeaders, "/widgets/abc", map[string]interface{}{"enabled": true})
	require.Error(t, err, "a stalled write must fail rather than hang")
	assert.Less(t, time.Since(start), 5*time.Second)
}

func TestJcGetRaw(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/widgets/abc", r.URL.Path)
		assert.Equal(t, "test-key", r.Header.Get("x-api-key"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "abc", "enabled": true})
	}))
	defer server.Close()

	result, err := jcGetRaw(server.URL, testHeaders, "/widgets/abc")
	require.NoError(t, err)
	assert.Equal(t, "abc", result["id"])
	assert.Equal(t, true, result["enabled"])
}

func TestJcGetRawNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	_, err := jcGetRaw(server.URL, testHeaders, "/widgets/missing")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestJcGetRawServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer server.Close()

	_, err := jcGetRaw(server.URL, testHeaders, "/widgets/abc")
	require.Error(t, err)
	assert.False(t, errors.Is(err, ErrNotFound))
	assert.Contains(t, err.Error(), "boom")
}

// v1 list endpoints (e.g. /organizations) wrap results in a "results" key --
// confirmed live and distinct from jcListArrayRaw's bare-array shape below.
func TestJcListRawWrapsResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"results": []map[string]interface{}{{"_id": "org1"}, {"_id": "org2"}},
		})
	}))
	defer server.Close()

	results, err := jcListRaw(server.URL, testHeaders, "/organizations")
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, "org1", results[0]["_id"])
}

// Regression test for the real bug fixed this session: /push/configs and
// /webauthn/configs return a bare JSON array, not {"results":[...]} like
// /organizations -- jcListRaw would fail to decode this shape, which is
// exactly why jcListArrayRaw exists as a separate helper.
func TestJcListArrayRawDecodesBareArray(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]interface{}{
			{"_id": "cfg1", "allowSelfRegistration": false},
		})
	}))
	defer server.Close()

	results, err := jcListArrayRaw(server.URL, testHeaders, "/webauthn/configs")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "cfg1", results[0]["_id"])
}

func TestJcListArrayRawRejectsWrappedShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"results": []map[string]interface{}{}})
	}))
	defer server.Close()

	_, err := jcListArrayRaw(server.URL, testHeaders, "/webauthn/configs")
	require.Error(t, err, "a {\"results\":[...]} body is not a bare array and must fail to decode")
}

func TestJcWriteRawMethods(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		method := method
		t.Run(method, func(t *testing.T) {
			var gotMethod string
			var gotBody map[string]interface{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod = r.Method
				if r.ContentLength != 0 {
					_ = json.NewDecoder(r.Body).Decode(&gotBody)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
			}))
			defer server.Close()

			result, err := jcWriteRaw(method, server.URL, testHeaders, "/thing/1", map[string]interface{}{"enabled": true})
			require.NoError(t, err)
			assert.Equal(t, method, gotMethod)
			assert.Equal(t, true, gotBody["enabled"])
			assert.Equal(t, true, result["ok"])
		})
	}
}

// Some v2 endpoints answer a successful write with 204 and an empty body --
// jcWriteRaw must treat that as success (nil, nil), not a decode error.
func TestJcWriteRawEmptyBodyIsSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	result, err := jcWriteRaw(http.MethodPut, server.URL, testHeaders, "/thing/1", map[string]interface{}{"enabled": true})
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestJcWriteRawUnsupportedMethod(t *testing.T) {
	_, err := jcWriteRaw(http.MethodGet, "http://example.invalid", testHeaders, "/thing/1", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported method")
}

// Regression test for the PATCH-vs-PUT bug: a captured browser request showed
// the real /authn/policies/{id} Update uses PATCH, not PUT (PUT 404s on a live
// id that GET succeeds on). jc-cli's generic update helper always uses PUT,
// which is wrong for this endpoint -- this test fails if AuthPolicyUpdateRaw
// ever regresses back to PUT.
func TestAuthPolicyUpdateRawUsesPatch(t *testing.T) {
	var gotMethod, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "policy1"})
	}))
	defer server.Close()

	_, err := AuthPolicyUpdateRaw(server.URL, testHeaders, "policy1", map[string]interface{}{"name": "test"})
	require.NoError(t, err)
	assert.Equal(t, http.MethodPatch, gotMethod)
	assert.Equal(t, "/authn/policies/policy1", gotPath)
}

func TestAuthPolicyUpdateRawEmptyBodyIsSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	result, err := AuthPolicyUpdateRaw(server.URL, testHeaders, "policy1", map[string]interface{}{"name": "test"})
	require.NoError(t, err)
	assert.Equal(t, map[string]interface{}{}, result)
}

// convertV2toV1Config must derive v1's BasePath from v2's, not leave it at
// jcapiv1's own hardcoded default -- otherwise any resource routed through it
// (jumpcloud_mfa_factor, jumpcloud_organization_settings) silently ignores a
// custom BasePath (as every test in this package relies on).
func TestConvertV2toV1ConfigBasePath(t *testing.T) {
	v2config := jcapiv2.NewConfiguration()

	v2config.BasePath = "https://console.jumpcloud.com/api/v2"
	v1config := convertV2toV1Config(v2config)
	assert.Equal(t, "https://console.jumpcloud.com/api", v1config.BasePath,
		"production v2 default must map to v1's real default, not just pass through unchanged")

	v2config.BasePath = "http://127.0.0.1:54321"
	v1config = convertV2toV1Config(v2config)
	assert.Equal(t, "http://127.0.0.1:54321", v1config.BasePath,
		"a BasePath with no /v2 suffix (e.g. a test server) must pass through unchanged")
}
