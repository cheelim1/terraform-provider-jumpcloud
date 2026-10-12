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

func TestResourceDefaultAccessPolicyCreateUpdate(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPut {
			gotMethod = r.Method
			gotPath = r.URL.Path
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
		}
		// Create calls Read afterward (a GET) -- reply consistently either way.
		_ = json.NewEncoder(w).Encode(gotBody)
	}))
	defer server.Close()

	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL
	config.AddDefaultHeader("x-api-key", "test-key")

	d := schema.TestResourceDataRaw(t, resourceDefaultAccessPolicy().Schema, map[string]interface{}{
		"resource_type":     "userportal",
		"effect_action":     "allow",
		"mfa_required":      true,
		"user_verification": "none",
	})

	require.NoError(t, resourceDefaultAccessPolicyCreateUpdate(d, config))

	assert.Equal(t, http.MethodPut, gotMethod)
	assert.Equal(t, "/authn/policy/fallback/userportal", gotPath)
	assert.Equal(t, map[string]interface{}{
		"effect": map[string]interface{}{
			"action": "allow",
			"obligations": map[string]interface{}{
				"mfa":              map[string]interface{}{"required": true},
				"userVerification": map[string]interface{}{"requirement": "none"},
			},
		},
	}, gotBody)
	assert.Equal(t, "userportal", d.Id())
}

func TestResourceDefaultAccessPolicyRead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/authn/policy/fallback/adminportal", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"effect": map[string]interface{}{
				"action": "allow",
				"obligations": map[string]interface{}{
					"mfa":              map[string]interface{}{"required": true},
					"userVerification": map[string]interface{}{"requirement": "preferred"},
				},
			},
		})
	}))
	defer server.Close()

	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL
	config.AddDefaultHeader("x-api-key", "test-key")

	d := schema.TestResourceDataRaw(t, resourceDefaultAccessPolicy().Schema, map[string]interface{}{})
	d.SetId("adminportal")

	require.NoError(t, resourceDefaultAccessPolicyRead(d, config))
	assert.Equal(t, "adminportal", d.Get("resource_type"))
	assert.Equal(t, "allow", d.Get("effect_action"))
	assert.Equal(t, true, d.Get("mfa_required"))
	assert.Equal(t, "preferred", d.Get("user_verification"))
}

func TestResourceDefaultAccessPolicyReadNotFoundClearsId(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL
	config.AddDefaultHeader("x-api-key", "test-key")

	d := schema.TestResourceDataRaw(t, resourceDefaultAccessPolicy().Schema, map[string]interface{}{})
	d.SetId("ldap")

	require.NoError(t, resourceDefaultAccessPolicyRead(d, config))
	assert.Equal(t, "", d.Id())
}

// This is a true singleton JumpCloud always has one of per resource type --
// Delete resets it to the safe default (no MFA) rather than deleting
// anything.
func TestResourceDefaultAccessPolicyDeleteResetsToSafeDefault(t *testing.T) {
	var gotBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gotBody)
	}))
	defer server.Close()

	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL
	config.AddDefaultHeader("x-api-key", "test-key")

	d := schema.TestResourceDataRaw(t, resourceDefaultAccessPolicy().Schema, map[string]interface{}{
		"resource_type": "application",
		"mfa_required":  true,
	})
	d.SetId("application")

	require.NoError(t, resourceDefaultAccessPolicyDelete(d, config))
	effect := gotBody["effect"].(map[string]interface{})
	assert.Equal(t, "allow", effect["action"])
	obligations := effect["obligations"].(map[string]interface{})
	assert.Equal(t, false, obligations["mfa"].(map[string]interface{})["required"])
	assert.Equal(t, "", d.Id())
}
