package jumpcloud

// Drives the real Terraform core (in-process, via SDK v1's helper/resource)
// through plan/apply/refresh/import/update/destroy for every MFA resource
// against a stateful fake JumpCloud API. Hermetic: no network, no TF_ACC.
// It catches schema-level problems the per-function unit tests can't --
// perpetual diffs, import round-trips, plan-time validation, drift detection,
// and what destroy leaves behind.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/terraform"
)

type fakeJumpCloud struct {
	mu       sync.Mutex
	objs     map[string]map[string]interface{}
	policies map[string]map[string]interface{}
	nextID   int
	log      []string
}

func newFakeJumpCloud() *fakeJumpCloud {
	return &fakeJumpCloud{
		objs: map[string]map[string]interface{}{
			"/v2/mfa/enrollmentPolicy": {"requirePrimaryFactor": false, "gracePeriodDays": float64(0), "requireBackupFactor": false},
			"/v2/durt/settings":        {"enabled": false, "readOnly": false},
			"/v2/webauthn/configs/w1":  {"_id": "w1", "allowSelfRegistration": false},
			"/v2/push/configs/p1": {"id": "p1", "userVerification": "discouraged", "isTotpIntegrated": true,
				"exclusionGroups": []interface{}{}, "readOnly": false, "unmanaged": "keep",
				"concurrentLimit": map[string]interface{}{"enabled": true, "max": float64(1)}},
			"/userportal/mfa/totp":     {"type": "totp", "enabled": true, "id": "", "readOnly": false},
			"/userportal/mfa/webauthn": {"type": "webauthn", "enabled": false, "id": "", "readOnly": false},
			"/organizations/org1": {"_id": "org1", "displayName": "Org", "settings": map[string]interface{}{
				"requireAdminMFA": false, "passwordPolicy": map[string]interface{}{"minLength": float64(12)}}},
		},
		policies: map[string]map[string]interface{}{},
	}
}

func (f *fakeJumpCloud) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, r.Method+" "+r.URL.Path)
	w.Header().Set("Content-Type", "application/json")
	var body map[string]interface{}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	p := r.URL.Path
	write := func(v interface{}) { _ = json.NewEncoder(w).Encode(v) }

	switch {
	case p == "/organizations" && r.Method == http.MethodGet:
		write(map[string]interface{}{"totalCount": 1, "results": []interface{}{f.objs["/organizations/org1"]}})
		return
	case p == "/v2/webauthn/configs":
		write([]interface{}{f.objs["/v2/webauthn/configs/w1"]})
		return
	case p == "/v2/push/configs":
		write([]interface{}{f.objs["/v2/push/configs/p1"]})
		return
	case strings.HasPrefix(p, "/v2/authn/policy/fallback/"):
		rt := strings.TrimPrefix(p, "/v2/authn/policy/fallback/")
		switch rt {
		case "application", "userportal", "adminportal", "ldap":
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method == http.MethodPut {
			f.objs[p] = body
		}
		if f.objs[p] == nil {
			f.objs[p] = map[string]interface{}{"effect": map[string]interface{}{"action": "allow",
				"obligations": map[string]interface{}{"mfa": map[string]interface{}{"required": false},
					"userVerification": map[string]interface{}{"requirement": "none"}}}}
		}
		write(f.objs[p])
		return
	case p == "/v2/authn/policies" && r.Method == http.MethodPost:
		f.nextID++
		id := fmt.Sprintf("pol%d", f.nextID)
		body["id"] = id
		f.policies[id] = body
		write(body)
		return
	case strings.HasPrefix(p, "/v2/authn/policies/"):
		id := strings.TrimPrefix(p, "/v2/authn/policies/")
		pol, ok := f.policies[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		switch r.Method {
		case http.MethodGet:
			write(pol)
		case http.MethodPatch:
			body["id"] = id
			f.policies[id] = body
			write(body)
		case http.MethodDelete:
			delete(f.policies, id)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound) // PUT 404s on the real API
		}
		return
	}

	obj, ok := f.objs[p]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if r.Method == http.MethodPut {
		if strings.HasPrefix(p, "/v2/webauthn/configs/") || strings.HasPrefix(p, "/userportal/mfa/") {
			for k, v := range body { // partial update
				obj[k] = v
			}
		} else {
			f.objs[p] = body
			obj = body
		}
	}
	write(obj)
}

func (f *fakeJumpCloud) get(path string) map[string]interface{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.objs[path]
}

func mfaLifecycleProviders(url string) map[string]terraform.ResourceProvider {
	p := Provider()
	p.ConfigureFunc = func(d *schema.ResourceData) (interface{}, error) {
		c := jcapiv2.NewConfiguration()
		c.BasePath = url + "/v2"
		c.AddDefaultHeader("x-api-key", d.Get("api_key").(string))
		return c, nil
	}
	return map[string]terraform.ResourceProvider{"jumpcloud": p}
}

const mfaLifecycleConfig = `
provider "jumpcloud" { api_key = "k" }
resource "jumpcloud_organization_settings" "o" {
  org_id            = "org1"
  require_admin_mfa = true
}
resource "jumpcloud_mfa_enrollment_policy" "e" {
  require_primary_factor = true
  grace_period_days      = 7
}
resource "jumpcloud_mfa_factor" "w" { factor_type = "webauthn" }
resource "jumpcloud_webauthn_settings" "w" { allow_self_registration = true }
resource "jumpcloud_push_settings" "p" {
  user_verification    = "required"
  concurrent_limit_max = 3
  exclusion_groups     = ["g1"]
}
resource "jumpcloud_durt_settings" "d" { enabled = true }
resource "jumpcloud_default_access_policy" "up" {
  resource_type = "userportal"
  mfa_required  = true
}
resource "jumpcloud_authentication_policy" "a" {
  name                  = "Require MFA"
  effect_action         = "allow"
  mfa_required          = true
  mfa_factors           = ["TOTP", "PUSH"]
  target_resource_types = ["user_portal"]
  target_user_inclusions = ["all"]
  conditions_json       = <<EOT
{
  "all": []
}
EOT
}
`

func TestMfaResourcesLifecycle(t *testing.T) {
	t.Setenv("JUMPCLOUD_API_KEY", "k")
	f := newFakeJumpCloud()
	srv := httptest.NewServer(f)
	defer srv.Close()

	resource.UnitTest(t, resource.TestCase{
		Providers: mfaLifecycleProviders(srv.URL),
		CheckDestroy: func(*terraform.State) error {
			fb := f.get("/v2/authn/policy/fallback/userportal")["effect"].(map[string]interface{})
			if fb["obligations"].(map[string]interface{})["mfa"].(map[string]interface{})["required"] != false {
				return fmt.Errorf("fallback not reset")
			}
			if f.get("/v2/mfa/enrollmentPolicy")["requirePrimaryFactor"] != false {
				return fmt.Errorf("enrollment not reset")
			}
			if f.get("/userportal/mfa/webauthn")["enabled"] != false {
				return fmt.Errorf("factor not disabled")
			}
			if f.get("/v2/push/configs/p1")["userVerification"] != "required" {
				return fmt.Errorf("push should be left as last applied")
			}
			org := f.get("/organizations/org1")["settings"].(map[string]interface{})
			if org["requireAdminMFA"] != false || org["passwordPolicy"] == nil {
				return fmt.Errorf("org settings wrong after destroy: %v", org)
			}
			if len(f.policies) != 0 {
				return fmt.Errorf("policy not deleted")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: mfaLifecycleConfig,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jumpcloud_authentication_policy.a", "mfa_factors.#", "2"),
					resource.TestCheckResourceAttr("jumpcloud_push_settings.p", "config_id", "p1"),
					resource.TestCheckResourceAttr("jumpcloud_webauthn_settings.w", "id", "w1"),
					resource.TestCheckResourceAttr("jumpcloud_default_access_policy.up", "mfa_required", "true"),
					func(*terraform.State) error {
						if f.get("/v2/push/configs/p1")["unmanaged"] != "keep" {
							return fmt.Errorf("push unmanaged field lost")
						}
						return nil
					},
				),
			},
			// Plan-time validation.
			{
				Config:      strings.Replace(mfaLifecycleConfig, `resource_type = "userportal"`, `resource_type = "user_portal"`, 1),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`expected resource_type to be one of`),
			},
			{
				Config:      strings.Replace(mfaLifecycleConfig, "grace_period_days      = 7", "grace_period_days      = -1", 1),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`expected grace_period_days to be at least`),
			},
			// Import every resource and verify state round-trips.
			{ResourceName: "jumpcloud_default_access_policy.up", Config: mfaLifecycleConfig, ImportState: true, ImportStateVerify: true},
			{ResourceName: "jumpcloud_mfa_enrollment_policy.e", Config: mfaLifecycleConfig, ImportState: true, ImportStateVerify: true},
			{ResourceName: "jumpcloud_mfa_factor.w", Config: mfaLifecycleConfig, ImportState: true, ImportStateVerify: true},
			{ResourceName: "jumpcloud_webauthn_settings.w", Config: mfaLifecycleConfig, ImportState: true, ImportStateVerify: true},
			{ResourceName: "jumpcloud_push_settings.p", Config: mfaLifecycleConfig, ImportState: true, ImportStateVerify: true},
			{ResourceName: "jumpcloud_durt_settings.d", Config: mfaLifecycleConfig, ImportState: true, ImportStateVerify: true},
			{ResourceName: "jumpcloud_organization_settings.o", Config: mfaLifecycleConfig, ImportState: true, ImportStateVerify: true},
			{ResourceName: "jumpcloud_authentication_policy.a", Config: mfaLifecycleConfig, ImportState: true, ImportStateVerify: true},
			// Out-of-band change: someone turns MFA off on the fallback in the
			// console, and the API stops returning the mfa object. Refresh must
			// show drift (non-empty plan) rather than keep the stale true.
			{
				PreConfig: func() {
					f.mu.Lock()
					f.objs["/v2/authn/policy/fallback/userportal"] = map[string]interface{}{
						"effect": map[string]interface{}{"action": "allow", "obligations": map[string]interface{}{}}}
					f.mu.Unlock()
				},
				Config:             mfaLifecycleConfig,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			// Re-apply converges.
			{Config: mfaLifecycleConfig},
			// Update in place.
			{
				Config: strings.Replace(strings.Replace(mfaLifecycleConfig, `mfa_factors           = ["TOTP", "PUSH"]`, `mfa_factors           = ["TOTP"]`, 1),
					"grace_period_days      = 7", "grace_period_days      = 14", 1),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jumpcloud_authentication_policy.a", "mfa_factors.#", "1"),
					resource.TestCheckResourceAttr("jumpcloud_mfa_enrollment_policy.e", "grace_period_days", "14"),
				),
			},
		},
	})
}
