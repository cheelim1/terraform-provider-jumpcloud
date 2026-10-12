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

func TestResourceMfaFactorCreateUpdate(t *testing.T) {
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
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"type": gotBody["type"], "enabled": gotBody["enabled"], "readOnly": false,
		})
	}))
	defer server.Close()

	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL
	config.AddDefaultHeader("x-api-key", "test-key")

	d := schema.TestResourceDataRaw(t, resourceMfaFactor().Schema, map[string]interface{}{
		"factor_type": "webauthn",
		"enabled":     true,
	})

	require.NoError(t, resourceMfaFactorCreateUpdate(d, config))

	assert.Equal(t, http.MethodPut, gotMethod)
	// "userportal" here is JumpCloud's internal naming for this admin-facing
	// API area, not an indication of per-user scope -- confirmed org-wide.
	assert.Equal(t, "/userportal/mfa/webauthn", gotPath)
	assert.Equal(t, map[string]interface{}{"type": "webauthn", "enabled": true}, gotBody)
	assert.Equal(t, "webauthn", d.Id())
}

func TestResourceMfaFactorRead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/userportal/mfa/totp", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"type": "totp", "enabled": true, "id": "", "readOnly": false,
		})
	}))
	defer server.Close()

	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL
	config.AddDefaultHeader("x-api-key", "test-key")

	d := schema.TestResourceDataRaw(t, resourceMfaFactor().Schema, map[string]interface{}{})
	d.SetId("totp")

	require.NoError(t, resourceMfaFactorRead(d, config))
	assert.Equal(t, "totp", d.Get("factor_type"))
	assert.Equal(t, true, d.Get("enabled"))
	assert.Equal(t, false, d.Get("read_only"))
}

// GET is confirmed to work for webauthn and totp, but not every factor_type
// is verified -- Read must not fail plan/apply over an unverified one, per
// the resource's own documented behavior; it should log and leave state as
// the source of truth instead of erroring or clearing the id.
func TestResourceMfaFactorReadToleratesUnverifiedFactorError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("unsupported factor"))
	}))
	defer server.Close()

	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL
	config.AddDefaultHeader("x-api-key", "test-key")

	d := schema.TestResourceDataRaw(t, resourceMfaFactor().Schema, map[string]interface{}{
		"factor_type": "sms",
		"enabled":     true,
	})
	d.SetId("sms")

	require.NoError(t, resourceMfaFactorRead(d, config), "a non-404 GET error must not fail Read")
	assert.Equal(t, "sms", d.Id(), "id must be left in place, not cleared, on an unverified-factor error")
}

func TestResourceMfaFactorReadNotFoundClearsId(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL
	config.AddDefaultHeader("x-api-key", "test-key")

	d := schema.TestResourceDataRaw(t, resourceMfaFactor().Schema, map[string]interface{}{
		"factor_type": "webauthn",
	})
	d.SetId("webauthn")

	require.NoError(t, resourceMfaFactorRead(d, config))
	assert.Equal(t, "", d.Id())
}

func TestResourceMfaFactorDeleteDisables(t *testing.T) {
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

	d := schema.TestResourceDataRaw(t, resourceMfaFactor().Schema, map[string]interface{}{
		"factor_type": "webauthn",
		"enabled":     true,
	})
	d.SetId("webauthn")

	require.NoError(t, resourceMfaFactorDelete(d, config))
	assert.Equal(t, false, gotBody["enabled"])
	assert.Equal(t, "", d.Id())
}
