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

func TestResourceDurtSettingsCreateUpdatePreservesReadOnly(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"enabled": false, "readOnly": true})
		case http.MethodPut:
			gotMethod = r.Method
			gotPath = r.URL.Path
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			_ = json.NewEncoder(w).Encode(gotBody)
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	}))
	defer server.Close()

	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL
	config.AddDefaultHeader("x-api-key", "test-key")

	d := schema.TestResourceDataRaw(t, resourceDurtSettings().Schema, map[string]interface{}{
		"enabled": true,
	})

	require.NoError(t, resourceDurtSettingsCreateUpdate(d, config))

	assert.Equal(t, http.MethodPut, gotMethod)
	assert.Equal(t, "/durt/settings", gotPath)
	assert.Equal(t, map[string]interface{}{"enabled": true, "readOnly": true}, gotBody,
		"server-controlled readOnly must be preserved from the prior GET")
	assert.Equal(t, durtSettingsID, d.Id())
}

func TestResourceDurtSettingsRead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/durt/settings", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"enabled": true, "readOnly": false})
	}))
	defer server.Close()

	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL
	config.AddDefaultHeader("x-api-key", "test-key")

	d := schema.TestResourceDataRaw(t, resourceDurtSettings().Schema, map[string]interface{}{})
	d.SetId(durtSettingsID)

	require.NoError(t, resourceDurtSettingsRead(d, config))
	assert.Equal(t, true, d.Get("enabled"))
}

func TestResourceDurtSettingsDeleteDisables(t *testing.T) {
	var gotBody map[string]interface{}
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"enabled": true, "readOnly": false})
		case http.MethodPut:
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			_ = json.NewEncoder(w).Encode(gotBody)
		}
	}))
	defer server.Close()

	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL
	config.AddDefaultHeader("x-api-key", "test-key")

	d := schema.TestResourceDataRaw(t, resourceDurtSettings().Schema, map[string]interface{}{
		"enabled": true,
	})
	d.SetId(durtSettingsID)

	require.NoError(t, resourceDurtSettingsDelete(d, config))
	assert.Equal(t, false, gotBody["enabled"])
	assert.Equal(t, "", d.Id())
	assert.Equal(t, 2, requestCount, "Delete must GET to preserve readOnly, then PUT")
}
