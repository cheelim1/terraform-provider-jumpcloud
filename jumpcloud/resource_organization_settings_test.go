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

// requireAdminMFA is a writable field on the organization's existing
// "settings" object, which has other fields (e.g. passwordPolicy) this
// resource doesn't manage. Create must GET-then-PUT the whole organization
// object, preserving those unmanaged fields -- the same read-modify-write
// safety as jumpcloud_application's Update.
func TestResourceOrganizationSettingsCreatePreservesUnmanagedSettings(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/organizations":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"results": []map[string]interface{}{{"_id": "org1"}},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/organizations/org1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"_id": "org1",
				"settings": map[string]interface{}{
					"requireAdminMFA": false,
					"passwordPolicy":  map[string]interface{}{"minLength": float64(12)},
				},
			})
		case r.Method == http.MethodPut:
			gotMethod = r.Method
			gotPath = r.URL.Path
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			_ = json.NewEncoder(w).Encode(gotBody)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL
	config.AddDefaultHeader("x-api-key", "test-key")

	d := schema.TestResourceDataRaw(t, resourceOrganizationSettings().Schema, map[string]interface{}{
		"require_admin_mfa": true,
	})

	require.NoError(t, resourceOrganizationSettingsCreate(d, config))

	assert.Equal(t, http.MethodPut, gotMethod)
	assert.Equal(t, "/organizations/org1", gotPath)
	settings := gotBody["settings"].(map[string]interface{})
	assert.Equal(t, true, settings["requireAdminMFA"])
	assert.Equal(t, map[string]interface{}{"minLength": float64(12)}, settings["passwordPolicy"],
		"an unmanaged settings field must survive the read-modify-write untouched")
	assert.Equal(t, "org1", d.Id())
}

func TestResourceOrganizationSettingsCreateSkipsDiscoveryWithExplicitOrgId(t *testing.T) {
	var sawListCall bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/organizations" {
			sawListCall = true
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"_id": "org2", "settings": map[string]interface{}{}})
	}))
	defer server.Close()

	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL
	config.AddDefaultHeader("x-api-key", "test-key")

	d := schema.TestResourceDataRaw(t, resourceOrganizationSettings().Schema, map[string]interface{}{
		"org_id":            "org2",
		"require_admin_mfa": true,
	})

	require.NoError(t, resourceOrganizationSettingsCreate(d, config))
	assert.False(t, sawListCall, "an explicit org_id must skip the /organizations list call (forbidden for Service Accounts)")
	assert.Equal(t, "org2", d.Id())
}

func TestResourceOrganizationSettingsRead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/organizations/org1", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"_id":      "org1",
			"settings": map[string]interface{}{"requireAdminMFA": true},
		})
	}))
	defer server.Close()

	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL
	config.AddDefaultHeader("x-api-key", "test-key")

	d := schema.TestResourceDataRaw(t, resourceOrganizationSettings().Schema, map[string]interface{}{})
	d.SetId("org1")

	require.NoError(t, resourceOrganizationSettingsRead(d, config))
	assert.Equal(t, "org1", d.Get("org_id"))
	assert.Equal(t, true, d.Get("require_admin_mfa"))
}

// Delete resets requireAdminMFA to JumpCloud's own default (false) without
// deleting the organization itself, and must preserve unrelated settings
// fields the same way Create/Update do.
func TestResourceOrganizationSettingsDeletePreservesUnmanagedSettings(t *testing.T) {
	var gotBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"_id": "org1",
				"settings": map[string]interface{}{
					"requireAdminMFA": true,
					"passwordPolicy":  map[string]interface{}{"minLength": float64(12)},
				},
			})
		case http.MethodPut:
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			_ = json.NewEncoder(w).Encode(gotBody)
		}
	}))
	defer server.Close()

	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL
	config.AddDefaultHeader("x-api-key", "test-key")

	d := schema.TestResourceDataRaw(t, resourceOrganizationSettings().Schema, map[string]interface{}{
		"require_admin_mfa": true,
	})
	d.SetId("org1")

	require.NoError(t, resourceOrganizationSettingsDelete(d, config))
	settings := gotBody["settings"].(map[string]interface{})
	assert.Equal(t, false, settings["requireAdminMFA"])
	assert.Equal(t, map[string]interface{}{"minLength": float64(12)}, settings["passwordPolicy"])
	assert.Equal(t, "", d.Id())
}
