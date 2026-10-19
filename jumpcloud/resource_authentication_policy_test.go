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

func authPolicySchemaFixture() map[string]interface{} {
	return map[string]interface{}{
		"name":                          "Require MFA - User Portal",
		"type":                          "user_portal",
		"disabled":                      true,
		"effect_action":                 "allow",
		"mfa_required":                  true,
		"mfa_factors":                   []interface{}{"TOTP", "PUSH", "DURT", "WEBAUTHN"},
		"mfa_factor_selection_mode":     "all",
		"user_verification_requirement": "none",
		"target_resource_types":         []interface{}{"user_portal"},
		"target_user_inclusions":        []interface{}{"all"},
	}
}

// Regression test for the real schema, found only via a captured browser
// request after jc-cli's own auth-policies command (effect as a flat
// object with no "effect" key at all, then effect as a string enum) both
// produced bodies the live API rejected with a 400.
func TestResourceAuthenticationPolicyCreateBuildsRealSchema(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			gotMethod = r.Method
			gotPath = r.URL.Path
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			gotBody["id"] = "policy1"
		}
		// Create calls Read afterward (a GET) -- reply consistently either way.
		_ = json.NewEncoder(w).Encode(gotBody)
	}))
	defer server.Close()

	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL
	config.AddDefaultHeader("x-api-key", "test-key")

	d := schema.TestResourceDataRaw(t, resourceAuthenticationPolicy().Schema, authPolicySchemaFixture())

	require.NoError(t, resourceAuthenticationPolicyCreate(d, config))

	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "/authn/policies", gotPath)
	assert.Equal(t, "policy1", d.Id())

	effect, ok := gotBody["effect"].(map[string]interface{})
	require.True(t, ok, "effect must be an object, not a string enum")
	assert.Equal(t, "allow", effect["action"])
	obligations, ok := effect["obligations"].(map[string]interface{})
	require.True(t, ok)
	mfa, ok := obligations["mfa"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, true, mfa["required"])
	factors, ok := obligations["mfaFactors"].([]interface{})
	require.True(t, ok)
	require.Len(t, factors, 4)
	assert.Equal(t, "WEBAUTHN", factors[3].(map[string]interface{})["type"])

	targets, ok := gotBody["targets"].(map[string]interface{})
	require.True(t, ok)
	resources, ok := targets["resources"].([]interface{})
	require.True(t, ok)
	require.Len(t, resources, 1)
	assert.Equal(t, "user_portal", resources[0].(map[string]interface{})["type"])
	users, ok := targets["users"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, []interface{}{"all"}, users["inclusions"])

	// userGroups isn't exposed as a managed field, but is confirmed present
	// on the real object via a captured PATCH -- it must be sent empty, not
	// omitted, to match the real schema.
	userGroups, ok := targets["userGroups"].(map[string]interface{})
	require.True(t, ok, "userGroups must be present even though it's unmanaged")
	assert.Equal(t, []interface{}{}, userGroups["inclusions"])
	assert.Equal(t, []interface{}{}, userGroups["exclusions"])
}

func TestResourceAuthenticationPolicyCreateRejectsInvalidConditionsJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("must not make an API call when conditions_json fails to parse")
	}))
	defer server.Close()

	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL
	config.AddDefaultHeader("x-api-key", "test-key")

	values := authPolicySchemaFixture()
	values["conditions_json"] = "{not valid json"
	d := schema.TestResourceDataRaw(t, resourceAuthenticationPolicy().Schema, values)

	err := resourceAuthenticationPolicyCreate(d, config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "conditions_json is not valid JSON")
}

// Read stores a compact re-encoding of the API's conditions, so formatted
// but semantically identical config must not produce a perpetual diff.
func TestResourceAuthenticationPolicyConditionsJSONDiffIsSemantic(t *testing.T) {
	s := resourceAuthenticationPolicy().Schema["conditions_json"]
	require.NotNil(t, s.DiffSuppressFunc)
	assert.True(t, s.DiffSuppressFunc("conditions_json", `{"foo":true}`, "{ \"foo\": true }\n", nil))
	assert.False(t, s.DiffSuppressFunc("conditions_json", `{"foo":true}`, `{"foo":false}`, nil))

	require.NotNil(t, s.ValidateFunc)
	_, errs := s.ValidateFunc("{not valid json", "conditions_json")
	assert.NotEmpty(t, errs, "invalid JSON must be rejected at plan time")
	_, errs = s.ValidateFunc(`{"foo": true}`, "conditions_json")
	assert.Empty(t, errs)
}

// Regression test for the PATCH-vs-PUT bug: Update must GET the current
// object (read-modify-write, preserving fields this resource doesn't manage)
// and then PATCH, not PUT -- a live PUT 404'd on an id that GET succeeded on.
func TestResourceAuthenticationPolicyUpdateUsesPatchAndPreservesUnmanagedFields(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":                       "policy1",
				"name":                     "Require MFA - User Portal",
				"someUnmanagedServerField": "do-not-drop-me",
			})
		case http.MethodPatch:
			gotMethod = r.Method
			gotPath = r.URL.Path
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			_ = json.NewEncoder(w).Encode(gotBody)
		default:
			t.Fatalf("unexpected method: %s (expected GET then PATCH, never PUT)", r.Method)
		}
	}))
	defer server.Close()

	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL
	config.AddDefaultHeader("x-api-key", "test-key")

	d := schema.TestResourceDataRaw(t, resourceAuthenticationPolicy().Schema, authPolicySchemaFixture())
	d.SetId("policy1")

	require.NoError(t, resourceAuthenticationPolicyUpdate(d, config))

	assert.Equal(t, http.MethodPatch, gotMethod)
	assert.Equal(t, "/authn/policies/policy1", gotPath)
	assert.Equal(t, "do-not-drop-me", gotBody["someUnmanagedServerField"],
		"read-modify-write must preserve server fields this resource doesn't manage")
}

// A name-only update to an imported or console-edited policy must not change
// who is subject to it: group selectors, user exclusions, and other target and
// effect fields this resource doesn't manage have to survive the PATCH.
func TestResourceAuthenticationPolicyUpdatePreservesUnmanagedTargetsAndEffect(t *testing.T) {
	var gotBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "policy1",
				"effect": map[string]interface{}{
					"action":       "allow",
					"unmanagedKey": "keep-effect",
					"obligations": map[string]interface{}{
						"mfa":                 map[string]interface{}{"required": false, "unmanagedMfa": "keep-mfa"},
						"unmanagedObligation": "keep-obligation",
					},
				},
				"targets": map[string]interface{}{
					"unmanagedTarget": "keep-target",
					"users": map[string]interface{}{
						"inclusions": []interface{}{"all"},
						"exclusions": []interface{}{"user-excluded"},
					},
					"userGroups": map[string]interface{}{
						"inclusions": []interface{}{"group-in"},
						"exclusions": []interface{}{"group-out"},
					},
				},
			})
		case http.MethodPatch:
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

	values := authPolicySchemaFixture()
	values["name"] = "Renamed policy"
	d := schema.TestResourceDataRaw(t, resourceAuthenticationPolicy().Schema, values)
	d.SetId("policy1")

	require.NoError(t, resourceAuthenticationPolicyUpdate(d, config))

	targets := gotBody["targets"].(map[string]interface{})
	assert.Equal(t, "keep-target", targets["unmanagedTarget"])
	assert.Equal(t, []interface{}{"group-in"}, targets["userGroups"].(map[string]interface{})["inclusions"])
	assert.Equal(t, []interface{}{"group-out"}, targets["userGroups"].(map[string]interface{})["exclusions"])
	users := targets["users"].(map[string]interface{})
	assert.Equal(t, []interface{}{"user-excluded"}, users["exclusions"], "users.exclusions must survive")
	assert.Equal(t, []interface{}{"all"}, users["inclusions"], "managed inclusions still come from config")

	effect := gotBody["effect"].(map[string]interface{})
	assert.Equal(t, "keep-effect", effect["unmanagedKey"])
	obligations := effect["obligations"].(map[string]interface{})
	assert.Equal(t, "keep-obligation", obligations["unmanagedObligation"])
	mfa := obligations["mfa"].(map[string]interface{})
	assert.Equal(t, "keep-mfa", mfa["unmanagedMfa"])
	assert.Equal(t, true, mfa["required"], "managed fields are still applied from config")
	assert.Equal(t, "Renamed policy", gotBody["name"])
}

// With no existing policy (Create), userGroups is still sent empty, matching
// the real schema.
func TestResourceAuthenticationPolicyCreateDefaultsEmptyUserGroups(t *testing.T) {
	d := schema.TestResourceDataRaw(t, resourceAuthenticationPolicy().Schema, authPolicySchemaFixture())
	body, err := buildAuthPolicyRequestBody(nil, d)
	require.NoError(t, err)
	groups := body["targets"].(map[string]interface{})["userGroups"].(map[string]interface{})
	assert.Equal(t, []string{}, groups["inclusions"])
	assert.Equal(t, []string{}, groups["exclusions"])
}

func TestResourceAuthenticationPolicyRead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/authn/policies/policy1", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"name":        "Require MFA - User Portal",
			"description": "desc",
			"type":        "user_portal",
			"disabled":    true,
			"monitorOnly": false,
			"conditions":  map[string]interface{}{},
			"effect": map[string]interface{}{
				"action": "allow",
				"obligations": map[string]interface{}{
					"mfa":                    map[string]interface{}{"required": true},
					"userVerification":       map[string]interface{}{"requirement": "none"},
					"mfaFactorSelectionMode": "all",
					"mfaFactors": []interface{}{
						map[string]interface{}{"type": "TOTP"},
						map[string]interface{}{"type": "WEBAUTHN"},
					},
				},
			},
			"targets": map[string]interface{}{
				"resources": []interface{}{
					map[string]interface{}{"type": "user_portal"},
				},
				"users":                map[string]interface{}{"inclusions": []interface{}{"all"}},
				"excludedApplications": []interface{}{"app1"},
			},
		})
	}))
	defer server.Close()

	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL
	config.AddDefaultHeader("x-api-key", "test-key")

	d := schema.TestResourceDataRaw(t, resourceAuthenticationPolicy().Schema, map[string]interface{}{})
	d.SetId("policy1")

	require.NoError(t, resourceAuthenticationPolicyRead(d, config))
	assert.Equal(t, "Require MFA - User Portal", d.Get("name"))
	assert.Equal(t, "allow", d.Get("effect_action"))
	assert.Equal(t, true, d.Get("mfa_required"))
	assert.Equal(t, []interface{}{"TOTP", "WEBAUTHN"}, d.Get("mfa_factors"))
	assert.Equal(t, []interface{}{"user_portal"}, d.Get("target_resource_types"))
	assert.Equal(t, []interface{}{"all"}, d.Get("target_user_inclusions"))
	assert.Equal(t, []interface{}{"app1"}, d.Get("target_excluded_applications"))
}

func TestResourceAuthenticationPolicyDelete(t *testing.T) {
	var gotMethod, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL
	config.AddDefaultHeader("x-api-key", "test-key")

	d := schema.TestResourceDataRaw(t, resourceAuthenticationPolicy().Schema, map[string]interface{}{})
	d.SetId("policy1")

	require.NoError(t, resourceAuthenticationPolicyDelete(d, config))
	assert.Equal(t, http.MethodDelete, gotMethod)
	assert.Equal(t, "/authn/policies/policy1", gotPath)
	assert.Equal(t, "", d.Id())
}

func TestResourceAuthenticationPolicyDeleteNotFoundIsNotAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL
	config.AddDefaultHeader("x-api-key", "test-key")

	d := schema.TestResourceDataRaw(t, resourceAuthenticationPolicy().Schema, map[string]interface{}{})
	d.SetId("policy1")

	require.NoError(t, resourceAuthenticationPolicyDelete(d, config))
	assert.Equal(t, "", d.Id())
}
