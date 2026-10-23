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

// Unlike jumpcloud_webauthn_settings, a captured PUT here sent the full
// object back (id, userVerification, concurrentLimit, isTotpIntegrated,
// exclusionGroups, readOnly), including the server-controlled readOnly flag
// preserved from a prior GET. This test fails if Create/Update ever
// regresses to a minimal partial body or drops/guesses readOnly.
func TestResourcePushSettingsCreateSendsFullBodyPreservingReadOnly(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/push/configs":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{{"id": "pcfg1"}})
		case r.Method == http.MethodGet && r.URL.Path == "/push/configs/pcfg1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "pcfg1", "readOnly": true,
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

	d := schema.TestResourceDataRaw(t, resourcePushSettings().Schema, map[string]interface{}{
		"user_verification":        "preferred",
		"concurrent_limit_enabled": true,
		"concurrent_limit_max":     2,
		"is_totp_integrated":       true,
	})

	require.NoError(t, resourcePushSettingsCreateUpdate(d, config))

	assert.Equal(t, http.MethodPut, gotMethod)
	assert.Equal(t, "/push/configs/pcfg1", gotPath)
	assert.Equal(t, "pcfg1", gotBody["id"])
	assert.Equal(t, "preferred", gotBody["userVerification"])
	assert.Equal(t, true, gotBody["isTotpIntegrated"])
	assert.Equal(t, true, gotBody["readOnly"], "server-controlled readOnly must be preserved from the prior GET, not reset to false")
	assert.Equal(t, map[string]interface{}{"enabled": true, "max": float64(2)}, gotBody["concurrentLimit"])
	assert.Equal(t, "pcfg1", d.Id())
}

func TestResourcePushSettingsRead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/push/configs/pcfg1", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"userVerification": "required",
			"isTotpIntegrated": false,
			"exclusionGroups":  []interface{}{"group1"},
			"concurrentLimit":  map[string]interface{}{"enabled": false, "max": float64(5)},
		})
	}))
	defer server.Close()

	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL
	config.AddDefaultHeader("x-api-key", "test-key")

	d := schema.TestResourceDataRaw(t, resourcePushSettings().Schema, map[string]interface{}{})
	d.SetId("pcfg1")

	require.NoError(t, resourcePushSettingsRead(d, config))
	assert.Equal(t, "required", d.Get("user_verification"))
	assert.Equal(t, false, d.Get("is_totp_integrated"))
	assert.Equal(t, false, d.Get("concurrent_limit_enabled"))
	assert.Equal(t, 5, d.Get("concurrent_limit_max"))
	assert.Equal(t, []interface{}{"group1"}, d.Get("exclusion_groups"))
}

// There's nothing meaningful to reset a push config to on destroy -- it's a
// JumpCloud-created singleton, not something Terraform creates or deletes.
// Delete must only stop tracking it, never write anything.
func TestResourcePushSettingsDeleteDoesNotWrite(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("Delete must not make any API call, got: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL
	config.AddDefaultHeader("x-api-key", "test-key")

	d := schema.TestResourceDataRaw(t, resourcePushSettings().Schema, map[string]interface{}{})
	d.SetId("pcfg1")

	require.NoError(t, resourcePushSettingsDelete(d, config))
	assert.Equal(t, "", d.Id())
}

// If the prerequisite GET fails, the write must abort rather than PUT a full
// object with guessed server-controlled fields.
func TestResourcePushSettingsWriteAbortsOnGetError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("must not write after a failed GET, got: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer server.Close()

	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL
	config.AddDefaultHeader("x-api-key", "test-key")

	d := schema.TestResourceDataRaw(t, resourcePushSettings().Schema, map[string]interface{}{
		"config_id": "pcfg1",
	})

	err := resourcePushSettingsCreateUpdate(d, config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

// Fields this resource doesn't manage must survive the full-object PUT.
func TestResourcePushSettingsWritePreservesUnmanagedFields(t *testing.T) {
	var gotBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":              "pcfg1",
				"readOnly":        false,
				"someNewField":    "keep-me",
				"concurrentLimit": map[string]interface{}{"enabled": false, "max": float64(9), "window": float64(30)},
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

	d := schema.TestResourceDataRaw(t, resourcePushSettings().Schema, map[string]interface{}{
		"config_id":            "pcfg1",
		"concurrent_limit_max": 3,
	})

	require.NoError(t, resourcePushSettingsCreateUpdate(d, config))
	assert.Equal(t, "keep-me", gotBody["someNewField"])
	assert.Equal(t, map[string]interface{}{"enabled": true, "max": float64(3), "window": float64(30)}, gotBody["concurrentLimit"])
}

// Same guard as webauthn: never pick one of several push configs by list order.
func TestResourcePushSettingsCreateRejectsMultipleConfigs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/push/configs" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]interface{}{{"id": "p1"}, {"id": "p2"}})
	}))
	defer server.Close()

	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL
	config.AddDefaultHeader("x-api-key", "test-key")

	d := schema.TestResourceDataRaw(t, resourcePushSettings().Schema, map[string]interface{}{})

	err := resourcePushSettingsCreateUpdate(d, config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "returned 2 push configs")
	assert.Equal(t, "", d.Id())
}

func TestDiscoverSingletonConfigIdKeyOrder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/both":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{{"id": "a", "_id": "b"}})
		case "/underscore":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{{"_id": "b"}})
		case "/none":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{{"name": "x"}})
		case "/empty":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{})
		}
	}))
	defer server.Close()

	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL

	id, err := discoverSingletonConfigId(config, "/both", "x", "id", "_id")
	require.NoError(t, err)
	assert.Equal(t, "a", id)
	id, err = discoverSingletonConfigId(config, "/both", "x", "_id", "id")
	require.NoError(t, err)
	assert.Equal(t, "b", id)
	id, err = discoverSingletonConfigId(config, "/underscore", "x", "id", "_id")
	require.NoError(t, err)
	assert.Equal(t, "b", id)
	_, err = discoverSingletonConfigId(config, "/none", "x", "id", "_id")
	assert.ErrorContains(t, err, "could not determine x config id")
	_, err = discoverSingletonConfigId(config, "/empty", "x", "id", "_id")
	assert.ErrorContains(t, err, "no x configs visible")
}

func TestResourcePushSettingsDescriptionDisclosesDestroyBehavior(t *testing.T) {
	assert.Contains(t, resourcePushSettings().Description, "left exactly as last applied")
}
