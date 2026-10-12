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

func TestResourceMfaEnrollmentPolicyCreateUpdate(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPut {
			gotMethod = r.Method
			gotPath = r.URL.Path
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
		}
		// Create calls Read afterward (a GET) -- reply with the same body
		// either way, without letting that GET overwrite the captured PUT.
		_ = json.NewEncoder(w).Encode(gotBody)
	}))
	defer server.Close()

	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL
	config.AddDefaultHeader("x-api-key", "test-key")

	d := schema.TestResourceDataRaw(t, resourceMfaEnrollmentPolicy().Schema, map[string]interface{}{
		"require_primary_factor": true,
		"grace_period_days":      3,
		"require_backup_factor":  false,
	})

	err := resourceMfaEnrollmentPolicyCreateUpdate(d, config)
	require.NoError(t, err)

	assert.Equal(t, http.MethodPut, gotMethod)
	assert.Equal(t, "/mfa/enrollmentPolicy", gotPath)
	assert.Equal(t, map[string]interface{}{
		"requirePrimaryFactor": true,
		"gracePeriodDays":      float64(3),
		"requireBackupFactor":  false,
	}, gotBody)
	assert.Equal(t, mfaEnrollmentPolicyID, d.Id())
}

func TestResourceMfaEnrollmentPolicyRead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/mfa/enrollmentPolicy", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"requirePrimaryFactor": true,
			"gracePeriodDays":      float64(7),
			"requireBackupFactor":  true,
		})
	}))
	defer server.Close()

	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL
	config.AddDefaultHeader("x-api-key", "test-key")

	d := schema.TestResourceDataRaw(t, resourceMfaEnrollmentPolicy().Schema, map[string]interface{}{})
	d.SetId(mfaEnrollmentPolicyID)

	require.NoError(t, resourceMfaEnrollmentPolicyRead(d, config))
	assert.Equal(t, true, d.Get("require_primary_factor"))
	assert.Equal(t, 7, d.Get("grace_period_days"))
	assert.Equal(t, true, d.Get("require_backup_factor"))
}

// Destroying this resource resets JumpCloud's enrollment policy to its own
// default (everything off), rather than leaving the last-applied state in
// place -- confirmed by the resource's own doc comment.
func TestResourceMfaEnrollmentPolicyDeleteResetsToDefault(t *testing.T) {
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

	d := schema.TestResourceDataRaw(t, resourceMfaEnrollmentPolicy().Schema, map[string]interface{}{
		"require_primary_factor": true,
		"grace_period_days":      3,
	})
	d.SetId(mfaEnrollmentPolicyID)

	require.NoError(t, resourceMfaEnrollmentPolicyDelete(d, config))
	assert.Equal(t, map[string]interface{}{
		"requirePrimaryFactor": false,
		"gracePeriodDays":      float64(0),
		"requireBackupFactor":  false,
	}, gotBody)
	assert.Equal(t, "", d.Id())
}
