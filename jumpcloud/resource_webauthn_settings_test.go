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

// The captured PUT body here was a minimal partial object
// ({"allowSelfRegistration":true}), not the full config object -- unlike
// jumpcloud_push_settings. This test fails if Update ever regresses to
// sending a full read-modify-write body instead.
func TestResourceWebauthnSettingsCreateSendsMinimalPartialBody(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]interface{}
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/webauthn/configs":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"_id": "cfg1", "allowSelfRegistration": false},
			})
		case r.Method == http.MethodPut:
			gotMethod = r.Method
			gotPath = r.URL.Path
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			_ = json.NewEncoder(w).Encode(gotBody)
		case r.Method == http.MethodGet && r.URL.Path == "/webauthn/configs/cfg1":
			// Create calls Read afterward -- reply with the same body.
			_ = json.NewEncoder(w).Encode(gotBody)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL
	config.AddDefaultHeader("x-api-key", "test-key")

	d := schema.TestResourceDataRaw(t, resourceWebauthnSettings().Schema, map[string]interface{}{
		"allow_self_registration": true,
	})

	require.NoError(t, resourceWebauthnSettingsCreateUpdate(d, config))

	assert.Equal(t, http.MethodPut, gotMethod)
	assert.Equal(t, "/webauthn/configs/cfg1", gotPath)
	assert.Equal(t, map[string]interface{}{"allowSelfRegistration": true}, gotBody,
		"body must contain only the managed field, not a full object")
	assert.Equal(t, "cfg1", d.Id())
	assert.Equal(t, 3, requestCount, "expected: discovery GET, write PUT, then Read's GET")
}

func TestResourceWebauthnSettingsCreateSkipsDiscoveryWhenConfigIdSet(t *testing.T) {
	var sawListCall bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/webauthn/configs" {
			sawListCall = true
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"allowSelfRegistration": true})
	}))
	defer server.Close()

	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL
	config.AddDefaultHeader("x-api-key", "test-key")

	d := schema.TestResourceDataRaw(t, resourceWebauthnSettings().Schema, map[string]interface{}{
		"config_id":               "explicit-id",
		"allow_self_registration": true,
	})

	require.NoError(t, resourceWebauthnSettingsCreateUpdate(d, config))
	assert.False(t, sawListCall, "an explicit config_id must skip the discovery GET")
	assert.Equal(t, "explicit-id", d.Id())
}

func TestResourceWebauthnSettingsReadNotFoundClearsId(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL
	config.AddDefaultHeader("x-api-key", "test-key")

	d := schema.TestResourceDataRaw(t, resourceWebauthnSettings().Schema, map[string]interface{}{})
	d.SetId("cfg1")

	require.NoError(t, resourceWebauthnSettingsRead(d, config))
	assert.Equal(t, "", d.Id())
}

func TestResourceWebauthnSettingsDeleteDisablesSelfRegistration(t *testing.T) {
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

	d := schema.TestResourceDataRaw(t, resourceWebauthnSettings().Schema, map[string]interface{}{
		"allow_self_registration": true,
	})
	d.SetId("cfg1")

	require.NoError(t, resourceWebauthnSettingsDelete(d, config))
	assert.Equal(t, map[string]interface{}{"allowSelfRegistration": false}, gotBody)
	assert.Equal(t, "", d.Id())
}
