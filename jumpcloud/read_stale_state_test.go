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

// Read must not treat old state as current remote state. When a response omits
// a field, state resets to the field's default instead of keeping the stale
// value -- otherwise Terraform reports no drift for a setting the API no
// longer returns. Each case refreshes already-populated state against a
// response with the fields removed, rather than starting from empty state.
func TestReadsResetFieldsMissingFromResponse(t *testing.T) {
	cases := []struct {
		name     string
		resource *schema.Resource
		id       string
		populate map[string]interface{}
		read     func(*schema.ResourceData, interface{}) error
		want     map[string]interface{}
	}{
		{
			name:     "durt_settings",
			resource: resourceDurtSettings(),
			id:       durtSettingsID,
			populate: map[string]interface{}{"enabled": true},
			read:     resourceDurtSettingsRead,
			want:     map[string]interface{}{"enabled": false},
		},
		{
			name:     "webauthn_settings",
			resource: resourceWebauthnSettings(),
			id:       "wcfg1",
			populate: map[string]interface{}{"config_id": "wcfg1", "allow_self_registration": true},
			read:     resourceWebauthnSettingsRead,
			want:     map[string]interface{}{"allow_self_registration": false},
		},
		{
			name:     "mfa_enrollment_policy",
			resource: resourceMfaEnrollmentPolicy(),
			id:       mfaEnrollmentPolicyID,
			populate: map[string]interface{}{
				"require_primary_factor": true,
				"grace_period_days":      30,
				"require_backup_factor":  true,
			},
			read: resourceMfaEnrollmentPolicyRead,
			want: map[string]interface{}{
				"require_primary_factor": false,
				"grace_period_days":      0,
				"require_backup_factor":  false,
			},
		},
		{
			name:     "mfa_factor",
			resource: resourceMfaFactor(),
			id:       "webauthn",
			populate: map[string]interface{}{"factor_type": "webauthn", "enabled": false},
			read:     resourceMfaFactorRead,
			want:     map[string]interface{}{"enabled": true, "read_only": false},
		},
		{
			name:     "push_settings",
			resource: resourcePushSettings(),
			id:       "pcfg1",
			populate: map[string]interface{}{
				"config_id":                "pcfg1",
				"user_verification":        "required",
				"is_totp_integrated":       false,
				"concurrent_limit_enabled": false,
				"concurrent_limit_max":     5,
				"exclusion_groups":         []interface{}{"group1"},
			},
			read: resourcePushSettingsRead,
			want: map[string]interface{}{
				"user_verification":        "discouraged",
				"is_totp_integrated":       true,
				"concurrent_limit_enabled": true,
				"concurrent_limit_max":     1,
				"exclusion_groups":         []interface{}{},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{})
			}))
			defer server.Close()

			config := jcapiv2.NewConfiguration()
			config.BasePath = server.URL
			config.AddDefaultHeader("x-api-key", "test-key")

			d := schema.TestResourceDataRaw(t, c.resource.Schema, c.populate)
			d.SetId(c.id)

			require.NoError(t, c.read(d, config))
			for k, want := range c.want {
				assert.Equal(t, want, d.Get(k), k)
			}
		})
	}
}
