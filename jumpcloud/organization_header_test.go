package jumpcloud

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// JumpCloud rejects GET/PUT /organizations/{id} with
// 403 "organization selection not allowed via header" when the request also
// carries x-org-id: these endpoints already name the organization in the path.
// So the organization helpers must send the API key but never x-org-id, even
// when the provider is configured with an org_id (which every other raw call
// still sends, and which jumpcloud_application needs for its metadata URL).

func TestOrganizationRawHelpersOmitOrgHeader(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		assert.Equal(t, "test-key", r.Header.Get("x-api-key"), "%s %s must still carry the API key", r.Method, r.URL.Path)
		if _, present := r.Header["X-Org-Id"]; present {
			t.Errorf("%s %s must not send x-org-id, got %q", r.Method, r.URL.Path, r.Header.Get("X-Org-Id"))
			http.Error(w, `{"message":"Forbidden: organization selection not allowed via header"}`, http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"_id": "org-123", "settings": map[string]interface{}{}})
	}))
	defer server.Close()

	headers := map[string]string{"x-api-key": "test-key", "x-org-id": "org-123"}

	_, err := OrganizationGetRaw(server.URL, headers, "org-123")
	require.NoError(t, err)
	_, err = OrganizationUpdateRaw(server.URL, headers, "org-123", map[string]interface{}{"settings": map[string]interface{}{}})
	require.NoError(t, err)

	assert.Equal(t, []string{"GET /organizations/org-123", "PUT /organizations/org-123"}, seen)
	assert.Equal(t, "org-123", headers["x-org-id"],
		"the provider's shared header map must not be modified, other resources still need x-org-id")
}

// The same provider configuration must still scope every other raw call.
func TestOrganizationHeaderOmissionDoesNotAffectOtherRawCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "org-123", r.Header.Get("x-org-id"), "%s %s must keep x-org-id", r.Method, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	headers := map[string]string{"x-api-key": "test-key", "x-org-id": "org-123"}
	_, err := jcGetRaw(server.URL, headers, "/authn/policies/p1")
	require.NoError(t, err)
	_, err = ApplicationGetRaw(server.URL, headers, "app1")
	require.NoError(t, err)
}

// End to end through the resource, with the provider configured like a real
// single-org setup: an explicit org_id (sent as x-org-id on the provider's
// default headers) and the resource's org_id left to fall back to it.
func TestResourceOrganizationSettingsWithProviderOrgIdSendsNoOrgHeader(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, present := r.Header["X-Org-Id"]; present {
			t.Errorf("%s %s must not send x-org-id", r.Method, r.URL.Path)
			http.Error(w, `{"message":"Forbidden: organization selection not allowed via header"}`, http.StatusForbidden)
			return
		}
		assert.Equal(t, "/organizations/org-123", r.URL.Path)
		methods = append(methods, r.Method)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"_id":      "org-123",
			"settings": map[string]interface{}{"requireAdminMFA": false},
		})
	}))
	defer server.Close()

	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL
	config.AddDefaultHeader("x-api-key", "test-key")
	config.AddDefaultHeader("x-org-id", "org-123")

	d := schema.TestResourceDataRaw(t, resourceOrganizationSettings().Schema, map[string]interface{}{
		"require_admin_mfa": true,
	})

	require.NoError(t, resourceOrganizationSettingsCreate(d, config))
	assert.Equal(t, "org-123", d.Id())
	require.NoError(t, resourceOrganizationSettingsRead(d, config))
	require.NoError(t, resourceOrganizationSettingsUpdate(d, config))
	require.NoError(t, resourceOrganizationSettingsDelete(d, config))

	// Create (GET, PUT, re-read), Read (GET), Update (GET, PUT, re-read) and
	// Delete (GET, PUT): every one of them went through the header-free path.
	counts := map[string]int{}
	for _, m := range methods {
		counts[m]++
	}
	assert.Equal(t, map[string]int{"GET": 6, "PUT": 3}, counts)
}
