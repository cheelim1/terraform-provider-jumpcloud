package jumpcloud

import (
	"encoding/json"
	"errors"
	"fmt"

	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/helper/structure"
	"github.com/hashicorp/terraform-plugin-sdk/helper/validation"
)

// resourceAuthenticationPolicy manages a JumpCloud authentication policy
// (JumpCloud's conditional-access policy engine, at /authn/policies in the v2
// API). This is the current, documented mechanism for requiring MFA on user
// logins -- jcapi-go (this provider's Go SDK dependency, with no real code
// change since 2019) has no knowledge of this endpoint at all, and
// JumpCloud's own actively maintained CLI (TheJumpCloud/jc-cli) turned out
// not to be a reliable reference for this specific area either: its
// `auth-policies create` command builds a body shape that the live API
// rejects (confirmed live: a 400 "missing effect", then a second 400 with
// `effect` present as a string instead of an object), and its tests for that
// command run against a mocked local server, not the real API. Authentication
// Policies wasn't one of jc-cli's specially live-verified areas.
//
// The schema below is instead built from a captured browser request (the
// JumpCloud console's own Authentication Policies UI), which is the only
// confirmed-correct source for this endpoint's real shape.
func resourceAuthenticationPolicy() *schema.Resource {
	return &schema.Resource{
		Description: "Manages a JumpCloud authentication policy (conditional " +
			"access / MFA enforcement). Built from a captured browser request " +
			"against the JumpCloud console -- neither this provider's Go SDK nor " +
			"JumpCloud's own jc-cli reliably document this endpoint's shape.",
		Create: resourceAuthenticationPolicyCreate,
		Read:   resourceAuthenticationPolicyRead,
		Update: resourceAuthenticationPolicyUpdate,
		Delete: resourceAuthenticationPolicyDelete,
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},
		Schema: map[string]*schema.Schema{
			"name": {
				Description: "Policy name.",
				Type:        schema.TypeString,
				Required:    true,
			},
			"description": {
				Description: "Policy description.",
				Type:        schema.TypeString,
				Optional:    true,
			},
			"type": {
				Description: "Policy type, e.g. \"user_portal\" or \"admin\".",
				Type:        schema.TypeString,
				Optional:    true,
				Computed:    true,
			},
			"disabled": {
				Description: "Whether the policy is disabled.",
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
			},
			"monitor_only": {
				Description: "Evaluate the policy and log the outcome without " +
					"actually enforcing it. Useful for testing a policy's reach " +
					"before it affects logins.",
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
			},
			"conditions_json": {
				Description: "The policy's conditions tree, as a raw JSON string. " +
					"No typed schema is exposed for this -- confirmed via a " +
					"captured browser request to be an object ({} for no " +
					"conditions), but its shape beyond that isn't confirmed.",
				Type:         schema.TypeString,
				Optional:     true,
				Default:      "{}",
				ValidateFunc: validation.StringIsJSON,
				// Read stores the API's compact re-encoding, so compare
				// semantically rather than textually to avoid perpetual diffs
				// on formatted input.
				DiffSuppressFunc: structure.SuppressJsonDiff,
			},
			"effect_action": {
				Description: "The base access decision when this policy applies: " +
					"confirmed value from a live capture is \"allow\" (MFA and " +
					"other requirements are layered on via the mfa_* / " +
					"user_verification fields below, not via this value).",
				Type:     schema.TypeString,
				Required: true,
			},
			"mfa_required": {
				Description: "Require MFA under this policy.",
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
			},
			"mfa_factors": {
				Description: "Acceptable MFA factor types, e.g. [\"TOTP\", " +
					"\"PUSH\", \"DURT\"] (confirmed values from a live capture; " +
					"the full set of valid factor type strings is not confirmed).",
				Type:     schema.TypeList,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
			"mfa_factor_selection_mode": {
				Description: "How mfa_factors are combined, e.g. \"all\" " +
					"(confirmed value from a live capture; other possible values " +
					"are not confirmed).",
				Type:     schema.TypeString,
				Optional: true,
				Default:  "all",
			},
			"user_verification_requirement": {
				Description: "WebAuthn user verification requirement, e.g. " +
					"\"none\" (confirmed value from a live capture; other " +
					"possible values are not confirmed).",
				Type:     schema.TypeString,
				Optional: true,
				Default:  "none",
			},
			"target_resource_types": {
				Description: "Resource types this policy targets, e.g. " +
					"[\"user_portal\"].",
				Type:     schema.TypeList,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
			"target_user_inclusions": {
				Description: "Which users this policy targets: [\"all\"] for " +
					"everyone, or specific user/group identifiers (shape beyond " +
					"\"all\" is not confirmed).",
				Type:     schema.TypeList,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
			"target_excluded_applications": {
				Description: "Application IDs excluded from this policy's targets.",
				Type:        schema.TypeList,
				Optional:    true,
				Elem:        &schema.Schema{Type: schema.TypeString},
			},
		},
	}
}

func resourceAuthenticationPolicyCreate(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*jcapiv2.Configuration)
	headers := config.DefaultHeader

	body, err := buildAuthPolicyRequestBody(nil, d)
	if err != nil {
		return err
	}

	result, err := AuthPolicyCreateRaw(config.BasePath, headers, body)
	if err != nil {
		return err
	}

	id, _ := result["id"].(string)
	if id == "" {
		id, _ = result["_id"].(string)
	}
	if id == "" {
		return fmt.Errorf("authentication policy created but no id was returned: %+v", result)
	}
	d.SetId(id)
	return resourceAuthenticationPolicyRead(d, meta)
}

func resourceAuthenticationPolicyRead(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*jcapiv2.Configuration)
	headers := config.DefaultHeader

	raw, err := AuthPolicyGetRaw(config.BasePath, headers, d.Id())
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			d.SetId("")
			return nil
		}
		return err
	}

	// Every field is set unconditionally, using its effective default when the
	// response omits it (or sends null). Skipping absent fields would leave the
	// previous value in state and make a remote change invisible to Terraform.
	effect, _ := raw["effect"].(map[string]interface{})
	obligations, _ := effect["obligations"].(map[string]interface{})
	mfa, _ := obligations["mfa"].(map[string]interface{})
	userVerification, _ := obligations["userVerification"].(map[string]interface{})
	targets, _ := raw["targets"].(map[string]interface{})
	users, _ := targets["users"].(map[string]interface{})

	conditionsJSON := "{}"
	if cond := raw["conditions"]; cond != nil {
		b, err := json.Marshal(cond)
		if err != nil {
			return fmt.Errorf("re-encoding conditions for state: %w", err)
		}
		conditionsJSON = string(b)
	}

	values := map[string]interface{}{
		"name":                          stringOr(raw["name"], ""),
		"description":                   stringOr(raw["description"], ""),
		"type":                          stringOr(raw["type"], ""),
		"disabled":                      boolOr(raw["disabled"], false),
		"monitor_only":                  boolOr(raw["monitorOnly"], false),
		"conditions_json":               conditionsJSON,
		"effect_action":                 stringOr(effect["action"], ""),
		"mfa_required":                  boolOr(mfa["required"], false),
		"mfa_factors":                   typesFromObjects(obligations["mfaFactors"]),
		"mfa_factor_selection_mode":     stringOr(obligations["mfaFactorSelectionMode"], "all"),
		"user_verification_requirement": stringOr(userVerification["requirement"], "none"),
		"target_resource_types":         typesFromObjects(targets["resources"]),
		"target_user_inclusions":        stringSliceFromAny(users["inclusions"]),
		"target_excluded_applications":  stringSliceFromAny(targets["excludedApplications"]),
	}
	for k, v := range values {
		if err := d.Set(k, v); err != nil {
			return err
		}
	}

	return nil
}

// stringOr returns v as a string, or def when v is absent, null, or not a string.
func stringOr(v interface{}, def string) string {
	if s, ok := v.(string); ok {
		return s
	}
	return def
}

// boolOr returns v as a bool, or def when v is absent, null, or not a bool.
func boolOr(v interface{}, def bool) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return def
}

// typesFromObjects extracts the "type" of each object in a raw JSON array of
// objects (mfaFactors, targets.resources). A missing or non-array value yields
// an empty slice.
func typesFromObjects(raw interface{}) []string {
	arr, _ := raw.([]interface{})
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if m, ok := item.(map[string]interface{}); ok {
			if t, ok := m["type"].(string); ok {
				out = append(out, t)
			}
		}
	}
	return out
}

func resourceAuthenticationPolicyUpdate(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*jcapiv2.Configuration)
	headers := config.DefaultHeader

	current, err := AuthPolicyGetRaw(config.BasePath, headers, d.Id())
	if err != nil {
		return err
	}
	body, err := buildAuthPolicyRequestBody(current, d)
	if err != nil {
		return err
	}
	if _, err := AuthPolicyUpdateRaw(config.BasePath, headers, d.Id(), body); err != nil {
		return err
	}
	return resourceAuthenticationPolicyRead(d, meta)
}

func resourceAuthenticationPolicyDelete(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*jcapiv2.Configuration)
	headers := config.DefaultHeader

	if err := AuthPolicyDeleteRaw(config.BasePath, headers, d.Id()); err != nil {
		if errors.Is(err, ErrNotFound) {
			d.SetId("")
			return nil
		}
		return err
	}
	d.SetId("")
	return nil
}

// buildAuthPolicyRequestBody applies this resource's managed fields onto seed
// (nil for Create, or the policy's current raw state for Update) and returns
// the result as the request body -- the same read-modify-write pattern used
// for jumpcloud_application's Update, since the real policy object may have
// fields this resource doesn't expose.
func buildAuthPolicyRequestBody(seed map[string]interface{}, d *schema.ResourceData) (map[string]interface{}, error) {
	body := seed
	if body == nil {
		body = map[string]interface{}{}
	}

	body["name"] = d.Get("name").(string)
	body["description"] = d.Get("description").(string)
	body["disabled"] = d.Get("disabled").(bool)
	body["monitorOnly"] = d.Get("monitor_only").(bool)
	if v, ok := d.GetOk("type"); ok {
		body["type"] = v.(string)
	}

	condJSON := d.Get("conditions_json").(string)
	if condJSON == "" {
		condJSON = "{}"
	}
	var cond interface{}
	if err := json.Unmarshal([]byte(condJSON), &cond); err != nil {
		return nil, fmt.Errorf("conditions_json is not valid JSON: %w", err)
	}
	body["conditions"] = cond

	mfaFactors := make([]map[string]interface{}, 0)
	for _, f := range d.Get("mfa_factors").([]interface{}) {
		mfaFactors = append(mfaFactors, map[string]interface{}{"type": f.(string)})
	}
	// Merge the managed fields into whatever the policy already has, rather
	// than rebuilding these objects, so fields this resource doesn't expose
	// (userGroups selectors, users.exclusions, ...) survive an update.
	effect := subMap(body, "effect")
	effect["action"] = d.Get("effect_action").(string)
	obligations := subMap(effect, "obligations")
	subMap(obligations, "mfa")["required"] = d.Get("mfa_required").(bool)
	subMap(obligations, "userVerification")["requirement"] = d.Get("user_verification_requirement").(string)
	obligations["mfaFactors"] = mfaFactors
	obligations["mfaFactorSelectionMode"] = d.Get("mfa_factor_selection_mode").(string)

	targets := subMap(body, "targets")
	targets["resources"] = mergeResourceSelectors(targets["resources"], d.Get("target_resource_types").([]interface{}))
	subMap(targets, "users")["inclusions"] = stringSliceFromInterfaceList(d.Get("target_user_inclusions").([]interface{}))
	targets["excludedApplications"] = stringSliceFromInterfaceList(d.Get("target_excluded_applications").([]interface{}))
	// userGroups isn't a managed field. Preserve whatever the policy has; only
	// when it's absent, send it empty, matching the real schema as closely as
	// possible (confirmed present on the real object via a captured PATCH).
	userGroups := subMap(targets, "userGroups")
	if _, ok := userGroups["inclusions"]; !ok {
		userGroups["inclusions"] = []string{}
	}
	if _, ok := userGroups["exclusions"]; !ok {
		userGroups["exclusions"] = []string{}
	}

	return body, nil
}

// mergeResourceSelectors builds targets.resources from the configured resource
// types while keeping each existing selector's other fields -- notably the id
// of an application-scoped selector, which this resource can't represent in
// state. Each configured type reuses the next not-yet-used existing selector of
// that type; only a type with no existing selector gets a bare {"type": ...}.
// Without this, a name-only update to an imported application-scoped policy
// would drop its application ids and widen it to every application.
func mergeResourceSelectors(existing interface{}, types []interface{}) []interface{} {
	current, _ := existing.([]interface{})
	used := make([]bool, len(current))
	out := make([]interface{}, 0, len(types))
	for _, t := range types {
		typ := t.(string)
		var picked interface{}
		for i, sel := range current {
			m, ok := sel.(map[string]interface{})
			if !used[i] && ok && m["type"] == typ {
				used[i] = true
				picked = m
				break
			}
		}
		if picked == nil {
			picked = map[string]interface{}{"type": typ}
		}
		out = append(out, picked)
	}
	return out
}

// subMap returns m[key] as a map, creating (and storing) an empty one when it
// is absent or not an object, so callers can set managed fields on it in place.
func subMap(m map[string]interface{}, key string) map[string]interface{} {
	if sub, ok := m[key].(map[string]interface{}); ok {
		return sub
	}
	sub := map[string]interface{}{}
	m[key] = sub
	return sub
}

// stringSliceFromInterfaceList converts a Terraform TypeList of strings
// (as returned by d.Get) into a []string.
func stringSliceFromInterfaceList(raw []interface{}) []string {
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		out = append(out, v.(string))
	}
	return out
}

// stringSliceFromAny converts a raw JSON array value (as decoded into
// []interface{} by encoding/json) into a []string, for setting into a
// Terraform TypeList. A missing or non-array value yields an empty slice.
func stringSliceFromAny(raw interface{}) []string {
	arr, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
